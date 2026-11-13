package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/LankiYang/golden-hour/internal/certutil"
	"github.com/LankiYang/golden-hour/internal/game"
	"github.com/LankiYang/golden-hour/internal/transport"
	"github.com/pion/webrtc/v4"
)

func main() {
	addr := flag.String("addr", ":8443", "UDP WebTransport and TCP HTTPS listen address")
	httpAddr := flag.String("http-addr", "127.0.0.1:8080", "loopback HTTP development page; empty disables")
	publicURL := flag.String("public-url", "https://localhost:8443", "public HTTPS origin, used for WebTransport")
	origins := flag.String("origins", "http://localhost:8080,http://127.0.0.1:8080", "additional exact allowed browser origins")
	root := flag.String("root", ".", "directory containing index.html and client/")
	certFile := flag.String("cert", "", "production TLS certificate chain")
	keyFile := flag.String("key", "", "production TLS private key")
	rtcAddr := flag.String("rtc-addr", ":8444", "UDP WebRTC ICE listen address")
	rtcPublicIP := flag.String("rtc-public-ip", "", "public IP advertised by WebRTC when behind NAT")
	iceJSON := flag.String("ice-servers", os.Getenv("GH_ICE_SERVERS"), "WebRTC ICE servers as JSON (or GH_ICE_SERVERS), including optional TURN credentials")
	flag.Parse()
	if *rtcPublicIP != "" && net.ParseIP(*rtcPublicIP) == nil {
		log.Fatal("-rtc-public-ip must be an IP address")
	}
	var iceServers []webrtc.ICEServer
	if *iceJSON != "" {
		if err := json.Unmarshal([]byte(*iceJSON), &iceServers); err != nil {
			log.Fatal("invalid ICE server JSON")
		}
		// Validate URLs and TURN authentication before accepting game clients.
		pc, err := webrtc.NewPeerConnection(webrtc.Configuration{ICEServers: iceServers})
		if err != nil {
			log.Fatal("invalid ICE server configuration")
		}
		pc.Close()
	}
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
	server := transport.New(hub, transport.Config{Addr: *addr, HTTPAddr: *httpAddr, PublicURL: *publicURL, Root: abs, CertHash: hash, Dev: dev, TLS: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}}, Origins: allowed, RTCAddr: *rtcAddr, RTCPublicIP: *rtcPublicIP, ICEServers: iceServers})
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
