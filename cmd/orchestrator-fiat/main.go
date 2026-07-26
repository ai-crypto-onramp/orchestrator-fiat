package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/api"
	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/audit"
	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/config"
	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/domain"
	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/fraud"
	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/idempotency"
	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/logging"
	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/mpi"
	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/otel"
	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/rail"
	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/store"
	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/store/postgres"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func main() {
	log.Fatal(run())
}

func run() error {
	shutdown, err := otel.Init("payment-orchestration")
	if err != nil {
		return err
	}
	defer func() { _ = shutdown(context.Background()) }()

	cfg := config.FromEnv()
	srv := newService(cfg)
	mux := api.NewMux(srv)
	handler := otelhttp.NewHandler(srv.RequestLogMiddleware(mux), "payment-orchestration")
	addr := ":" + cfg.Port
	return http.ListenAndServe(addr, handler)
}

func newService(cfg config.Config) *api.Service {
	st := newStore()
	logger := logging.NewDefault()
	devMode := config.DevMode()
	if devMode {
		logger.Warn("DEV_MODE=1: stub clients in use — NOT FOR PRODUCTION", nil)
	}
	sink := newAuditSink(devMode)
	registry := newRailRegistry(cfg, devMode)
	mpiClient := newMPIClient(cfg, devMode)
	fraudClient := newFraudClient(cfg, devMode)
	svc := api.NewService(st, registry, sink, cfg.WebhookSecret("card"))
	svc.ApplyConfig(cfg)
	svc.MPI = mpiClient
	svc.Fraud = fraudClient
	svc.Logger = logger
	if idemStore := newIdempotencyStore(context.Background(), cfg, devMode); idemStore != nil {
		svc.WithIdempotencyStore(idemStore, cfg.IdempotencyKeyTTL)
	}
	return svc
}

// newIdempotencyStore builds the idempotency store from REDIS_URL. When
// REDIS_URL is set, a Redis-backed store is returned (SET NX with TTL, shared
// across replicas). When REDIS_URL is unset:
//   - DEV_MODE=1 or the test binary: an in-memory store is used with a warning
//     (state is per-process; replays across replicas are NOT deduplicated).
//   - otherwise: fatal at startup — silently falling back to in-memory in
//     production is a money-loss vector on retry across replicas.
func newIdempotencyStore(ctx context.Context, cfg config.Config, devMode bool) idempotency.Store {
	redisURL := os.Getenv("REDIS_URL")
	if redisURL != "" {
		st, err := idempotency.Open(ctx, redisURL)
		if err != nil {
			log.Fatalf("idempotency: open redis: %v — set REDIS_URL to a reachable Redis or unset it with DEV_MODE=1 for local dev", err)
		}
		log.Printf("idempotency: Redis-backed store enabled (REDIS_URL set, ttl=%s)", cfg.IdempotencyKeyTTL)
		return st
	}
	if devMode || testing.Testing() {
		log.Printf("warn: REDIS_URL unset and DEV_MODE=1 (or test binary); using in-memory idempotency store — replays across replicas are NOT deduplicated (NOT FOR PRODUCTION)")
		return idempotency.NewMem()
	}
	log.Fatal("REDIS_URL not set and DEV_MODE!=1; refusing to start in production mode — an in-memory idempotency store would lose dedup across replicas (money-loss on retry); set REDIS_URL or DEV_MODE=1 for local dev")
	return nil
}

// newRailRegistry builds the rail.Registry. Real per-rail HTTP adapters
// (rail.NewHTTP) are wired for every RAIL_*_URL configured. In DEV_MODE with
// no rail URLs configured, the DummyAdapter is used for all rails. In
// production at least one RAIL_*_URL must be set.
func newRailRegistry(cfg config.Config, devMode bool) *rail.Registry {
	byRail := make(map[domain.Rail]rail.Adapter)
	for r, url := range cfg.RailURLs {
		if url == "" {
			continue
		}
		byRail[r] = rail.NewHTTP(url, string(r))
	}
	if len(byRail) == 0 {
		if devMode {
			log.Printf("DEV_MODE=1: no RAIL_*_URL configured — using rail.NewDummy() for all rails — NOT FOR PRODUCTION")
			return rail.NewRegistry(rail.NewDummy())
		}
		log.Fatalf("no RAIL_*_URL configured and DEV_MODE!=1; set DEV_MODE=1 for local dev or provide RAIL_CARD_URL/RAIL_ACH_URL/RAIL_SEPA_URL/RAIL_PIX_URL/RAIL_UPI_URL")
	}
	var fallback rail.Adapter
	if devMode {
		fallback = rail.NewDummy()
	}
	return rail.NewRegistryFromMap(byRail, fallback)
}

// newMPIClient builds the 3DS MPI client. A real HTTP MPI client
// (mpi.NewHTTP) is used when THREE_DS_MPI_URL is set; otherwise the dummy is
// used only in DEV_MODE. In production THREE_DS_MPI_URL must be set.
func newMPIClient(cfg config.Config, devMode bool) mpi.Client {
	if cfg.ThreeDSMPIURL != "" {
		return mpi.NewHTTP(cfg.ThreeDSMPIURL)
	}
	if devMode {
		log.Printf("DEV_MODE=1: THREE_DS_MPI_URL unset — using mpi.NewDummy() — NOT FOR PRODUCTION")
		return mpi.NewDummy()
	}
	log.Fatalf("THREE_DS_MPI_URL not set and DEV_MODE!=1; real MPI client required in production — set DEV_MODE=1 for local dev")
	return nil
}

// newFraudClient builds the fraud client. A real HTTP client exists
// (fraud.NewHTTP) and is wired when FRAUD_URL is set; otherwise the dummy is
// used only in DEV_MODE.
func newFraudClient(cfg config.Config, devMode bool) fraud.Client {
	if cfg.FraudURL != "" {
		return fraud.NewHTTP(cfg.FraudURL)
	}
	if devMode {
		log.Printf("DEV_MODE=1: FRAUD_URL unset — using fraud.NewDummy() (NOT FOR PRODUCTION)")
		return fraud.NewDummy()
	}
	log.Fatalf("FRAUD_URL not set and DEV_MODE!=1; refusing to start in production mode — set DEV_MODE=1 for local dev")
	return nil
}

func newAuditSink(devMode bool) audit.Sink {
	brokers := os.Getenv("KAFKA_BROKERS")
	if brokers == "" {
		if devMode {
			log.Printf("warn: KAFKA_BROKERS unset and DEV_MODE=1; audit events recorded in-memory only")
			return audit.NewRecorder()
		}
		log.Fatalf("KAFKA_BROKERS unset and DEV_MODE not set; cannot start audit producer")
	}
	sink, err := audit.NewKafkaSink(splitCSV(brokers))
	if err != nil {
		if devMode {
			log.Printf("warn: audit kafka init failed (DEV_MODE): %v; falling back to recorder", err)
			return audit.NewRecorder()
		}
		log.Fatalf("audit kafka init: %v", err)
	}
	return sink
}

func splitCSV(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func newStore() store.Store {
	dsn := os.Getenv("DB_URL")
	if dsn != "" {
		db, err := postgres.Open(context.Background(), dsn)
		if err != nil {
			log.Fatalf("postgres: open: %v", err)
		}
		return db
	}
	if config.DevMode() {
		log.Printf("WARNING: DEV_MODE=1 with no DB_URL — using in-memory store; all state is lost on restart")
		return store.New()
	}
	config.MustEnvOrFatal("DB_URL", "DB_URL required in production mode — set DEV_MODE=1 to allow in-memory store for development")
	return store.New()
}
