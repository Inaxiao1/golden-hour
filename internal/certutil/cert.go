package certutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Development uses a short-lived P-256 certificate pinned by WebTransport.
// It neither changes the OS trust store nor requires bypassing a TLS warning.
func Load(certFile, keyFile, dir string) (tls.Certificate, string, bool, error) {
	dev := certFile == "" && keyFile == ""
	if !dev && (certFile == "" || keyFile == "") {
		return tls.Certificate{}, "", false, fmt.Errorf("both -cert and -key are required")
	}
	if dev {
		certFile = filepath.Join(dir, "dev-cert.pem")
		keyFile = filepath.Join(dir, "dev-key.pem")
		refresh := true
		if c, e := tls.LoadX509KeyPair(certFile, keyFile); e == nil {
			if leaf, e := x509.ParseCertificate(c.Certificate[0]); e == nil && time.Until(leaf.NotAfter) > 24*time.Hour {
				refresh = false
			}
		}
		if refresh {
			if err := os.MkdirAll(dir, 0700); err != nil {
				return tls.Certificate{}, "", true, err
			}
			key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				return tls.Certificate{}, "", true, err
			}
			serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
			if err != nil {
				return tls.Certificate{}, "", true, err
			}
			now := time.Now()
			template := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Golden Hour development"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(13 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}}
			der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
			if err != nil {
				return tls.Certificate{}, "", true, err
			}
			keyDER, err := x509.MarshalPKCS8PrivateKey(key)
			if err != nil {
				return tls.Certificate{}, "", true, err
			}
			if err = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
				return tls.Certificate{}, "", true, err
			}
			if err = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
				return tls.Certificate{}, "", true, err
			}
		}
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return cert, "", dev, err
	}
	hash := sha256.Sum256(cert.Certificate[0])
	return cert, base64.StdEncoding.EncodeToString(hash[:]), dev, nil
}
