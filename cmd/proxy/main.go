package main

import (
	"log"
	"net/http"
	"time"

	"ai-solana-proxy/internal/config"
	"ai-solana-proxy/internal/proxy"
	"ai-solana-proxy/internal/solana"
	"ai-solana-proxy/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	upstream, err := proxy.NewOpenAIProxy(cfg.OpenAIURL, cfg.OpenAIKey)
	if err != nil {
		log.Fatalf("configure OpenAI proxy: %v", err)
	}
	payments := &proxy.PaymentMiddleware{Validator: solana.NewValidator(cfg.RPCURL, time.Duration(cfg.RPCTimeout)*time.Second), Cache: store.NewMemoryCache(cfg.CacheSize), WalletPubKey: cfg.Wallet, Price: cfg.Price}
	mux := http.NewServeMux()
	mux.Handle("/", payments.Wrap(upstream))
	server := &http.Server{Addr: cfg.ListenAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("ProofPilot proxy listening on %s; RPC=%s", cfg.ListenAddr, cfg.RPCURL)
	log.Fatal(server.ListenAndServe())
}
