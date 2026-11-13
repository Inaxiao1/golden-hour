package main

import (
	"context"
	"crypto/tls"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/LankiYang/golden-hour/internal/certutil"
	"github.com/LankiYang/golden-hour/internal/game"
	"github.com/LankiYang/golden-hour/internal/transport"
)

func main() {
	addr := flag.String("addr", ":8443", "UDP WebTransport and TCP HTTPS listen address")
	httpAddr := flag.String("http-addr", "127.0.0.1:8080", "loopback HTTP development page; empty disables")
	publicURL := flag.String("public-url", "https://localhost:8443", "public HTTPS origin, used for WebTransport")
	origins := flag.String("origins", "http://localhost:8080,http://127.0.0.1:8080", "additional exact allowed browser origins")
	root := flag.String("root", ".", "directory containing index.html and client/")
	certFile := flag.String("cert", "", "production TLS certificate chain")
	keyFile := flag.String("key", "", "production TLS private key")
	flag.Parse()
	abs, err := filepath.Abs(*root)
	if err != nil {
		log.Fatal(err)
	}
	cert, hash, dev, err := certutil.Load(*certFile, *keyFile, filepath.Join(abs, ".local"))
	if err != nil {
		log.Fatal(err)
	}
	if !dev {
		hash = ""
		if *httpAddr != "" {
			log.Fatal("production TLS requires -http-addr='' to disable plaintext HTTP")
		}
	}
	allowed := []string{strings.TrimRight(*publicURL, "/")}
	for _, o := range strings.Split(*origins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			allowed = append(allowed, o)
		}
	}
	hub := game.NewHub(game.Hotel())
	server := transport.New(hub, transport.Config{Addr: *addr, HTTPAddr: *httpAddr, PublicURL: *publicURL, Root: abs, CertHash: hash, Dev: dev, TLS: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}}, Origins: allowed})
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	log.Printf("Golden Hour PvP: %s; authoritative 60 Hz / snapshots 30 Hz / %d static colliders", server, len(hub.World.Colliders))
	if dev {
		log.Printf("Open http://localhost:8080 (development certificate pinned by WebTransport)")
	}
	if err = server.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
