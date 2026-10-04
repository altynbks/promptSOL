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
	payments := &proxy.TokenBillingHandler{
		Upstream:         upstream,
		Validator:        solana.NewValidator(cfg.RPCURL, time.Duration(cfg.RPCTimeout)*time.Second),
		SignatureCache:   store.NewMemoryCache(cfg.CacheSize),
		Pending:          store.NewPendingPayments(32),
		Recipient:        cfg.Wallet,
		LamportsPerToken: cfg.LamportsPerToken,
	}
	mux := http.NewServeMux()
	web, err := fs.Sub(webFiles, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("POST /v1/chat/completions", payments)
	mux.HandleFunc("GET /api/payment-info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"pay_to": cfg.Wallet, "lamports_per_token": cfg.LamportsPerToken, "model": cfg.AIModel})
	})
	mux.Handle("/", http.FileServer(http.FS(web)))
	server := &http.Server{Addr: cfg.ListenAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 60 * time.Second}
	log.Printf("Solana x402 AI Proxy listening on %s; RPC=%s", cfg.ListenAddr, cfg.RPCURL)
	log.Fatal(server.ListenAndServe())
}
