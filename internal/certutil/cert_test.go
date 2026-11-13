package certutil

import (
	"crypto/ecdsa"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDevelopmentCertificatePinning(t *testing.T) {
	dir := t.TempDir()
	cert, hash, dev, err := Load("", "", dir)
	if err != nil || !dev || hash == "" {
		t.Fatal("development certificate", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := leaf.PublicKey.(*ecdsa.PublicKey); !ok {
		t.Fatal("certificate must use ECDSA for browser pinning")
	}
	if leaf.NotAfter.Sub(leaf.NotBefore) > 14*24*time.Hour || time.Now().Before(leaf.NotBefore) {
		t.Fatal("invalid WebTransport certificate validity")
	}
	if err := leaf.VerifyHostname("localhost"); err != nil {
		t.Fatal(err)
	}
	_, same, _, err := Load("", "", dir)
	if err != nil || same != hash {
		t.Fatal("certificate hash changed across restarts")
	}
	info, err := os.Stat(filepath.Join(dir, "dev-key.pem"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private key permissions", err)
	}
	if _, _, _, err = Load("missing.pem", "", dir); err == nil {
		t.Fatal("incomplete production keypair accepted")
	}
}
