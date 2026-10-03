package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"time"

	"ai-solana-proxy/internal/config"
	"ai-solana-proxy/internal/proxy"
	"ai-solana-proxy/internal/solana"
	"ai-solana-proxy/internal/store"
)

//go:embed web/*
var webFiles embed.FS

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	upstream, err := proxy.NewOpenAIProxy(cfg.UpstreamURL, cfg.UpstreamKey)
	if err != nil {
		log.Fatalf("configure AI upstream: %v", err)
	}
	payments := &proxy.PaymentMiddleware{Validator: solana.NewValidator(cfg.RPCURL, time.Duration(cfg.RPCTimeout)*time.Second), Cache: store.NewMemoryCache(cfg.CacheSize), WalletPubKey: cfg.Wallet, Price: cfg.Price}
	mux := http.NewServeMux()
	web, err := fs.Sub(webFiles, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("/v1/", payments.Wrap(upstream))
	mux.HandleFunc("GET /api/payment-info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"pay_to": cfg.Wallet, "amount": cfg.Price})
	})
	mux.Handle("/", http.FileServer(http.FS(web)))
	server := &http.Server{Addr: cfg.ListenAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("ProofPilot proxy listening on %s; RPC=%s", cfg.ListenAddr, cfg.RPCURL)
	log.Fatal(server.ListenAndServe())
}
