package mtls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTempCert(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("createcert: %v", err)
	}
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); err != nil {
		t.Fatalf("writecert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshalkey: %v", err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatalf("writekey: %v", err)
	}
	return
}

func writeBadCA(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(p, []byte("not a pem"), 0644); err != nil {
		t.Fatalf("writeca: %v", err)
	}
	return p
}

func writeGoodCA(t *testing.T, dir string) string {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("createca: %v", err)
	}
	p := filepath.Join(dir, "ca-good.pem")
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); err != nil {
		t.Fatalf("writeca: %v", err)
	}
	return p
}

func TestHTTPClientWithCertAndCA(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeTempCert(t, dir)
	caPath := writeGoodCA(t, dir)
	c := Config{CertFile: certPath, KeyFile: keyPath, CAFile: caPath}
	client, err := c.HTTPClient()
	if err != nil {
		t.Fatalf("HTTPClient: %v", err)
	}
	if client.Timeout == 0 {
		t.Fatal("expected timeout set")
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}
	if tr.TLSClientConfig == nil {
		t.Fatal("expected TLSClientConfig")
	}
	if tr.TLSClientConfig.RootCAs == nil {
		t.Fatal("expected RootCAs set")
	}
}

func TestHTTPClientWithCertNoCA(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeTempCert(t, dir)
	c := Config{CertFile: certPath, KeyFile: keyPath}
	client, err := c.HTTPClient()
	if err != nil {
		t.Fatalf("HTTPClient: %v", err)
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}
	if tr.TLSClientConfig == nil || len(tr.TLSClientConfig.Certificates) != 1 {
		t.Fatal("expected one client certificate")
	}
}

func TestHTTPClientBadCAFile(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeTempCert(t, dir)
	c := Config{CertFile: certPath, KeyFile: keyPath, CAFile: filepath.Join(dir, "missing-ca.pem")}
	if _, err := c.HTTPClient(); err == nil {
		t.Fatal("expected error for missing CA file")
	}
}

func TestHTTPClientUnparsableCA(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeTempCert(t, dir)
	caPath := writeBadCA(t, dir)
	c := Config{CertFile: certPath, KeyFile: keyPath, CAFile: caPath}
	if _, err := c.HTTPClient(); err == nil {
		t.Fatal("expected error for unparsable CA bundle")
	}
}

func TestHTTPClientInvalidKeyPair(t *testing.T) {
	dir := t.TempDir()
	c := Config{CertFile: filepath.Join(dir, "nope.pem"), KeyFile: filepath.Join(dir, "nope.pem")}
	if _, err := c.HTTPClient(); err == nil {
		t.Fatal("expected error for missing key pair")
	}
}