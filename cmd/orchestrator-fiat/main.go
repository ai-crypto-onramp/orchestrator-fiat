package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/api"
	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/audit"
	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/config"
	"github.com/ai-crypto-onramp/orchestrator-fiat/internal/fraud"
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
	return svc
}

// newRailRegistry builds the rail.Registry. A real per-rail HTTP connector
// implementation does not yet exist in this service; in production (DEV_MODE
// unset) we refuse to start with a clear message and require at least one
// RAIL_*_URL to be configured. When DEV_MODE=1 the DummyAdapter is used.
func newRailRegistry(cfg config.Config, devMode bool) *rail.Registry {
	if devMode {
		log.Printf("DEV_MODE=1: using rail.NewDummy() adapter for all rails — NOT FOR PRODUCTION")
		return rail.NewRegistry(rail.NewDummy())
	}
	enabled := cfg.EnabledRails()
	if len(enabled) == 0 {
		log.Fatalf("no RAIL_*_URL configured and DEV_MODE!=1; real rail client not yet implemented — set DEV_MODE=1 for local dev or provide RAIL_CARD_URL/RAIL_ACH_URL/RAIL_SEPA_URL/RAIL_PIX_URL/RAIL_UPI_URL")
	}
	log.Fatalf("real rail HTTP client not yet implemented (configured rails: %v) — set DEV_MODE=1 for local dev", enabled)
	return nil
}

// newMPIClient builds the 3DS MPI client. A real HTTP MPI client is not yet
// implemented; in production we require THREE_DS_MPI_URL and refuse to fall
// back to the dummy. When DEV_MODE=1 the DummyClient is used.
func newMPIClient(cfg config.Config, devMode bool) mpi.Client {
	if devMode {
		log.Printf("DEV_MODE=1: using mpi.NewDummy() — NOT FOR PRODUCTION")
		return mpi.NewDummy()
	}
	if cfg.ThreeDSMPIURL == "" {
		log.Fatalf("THREE_DS_MPI_URL not set and DEV_MODE!=1; real MPI client not yet implemented — set DEV_MODE=1 for local dev")
	}
	log.Fatalf("real MPI HTTP client not yet implemented (THREE_DS_MPI_URL=%s) — set DEV_MODE=1 for local dev", cfg.ThreeDSMPIURL)
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
