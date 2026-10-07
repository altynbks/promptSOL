package main

import (
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
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
	primaryProvider := "Gemini"
	primaryModel := cfg.GeminiModel
	var upstream *httputil.ReverseProxy
	if cfg.GroqKey != "" {
		primaryProvider = "Groq"
		primaryModel = cfg.GroqModel
		upstream, err = proxy.NewRoutedOpenAIProxy(cfg.GroqURL, cfg.GroqKey, "Groq", cfg.GroqModel, cfg.UpstreamURL, cfg.UpstreamKey, "Gemini", cfg.GeminiModel)
	} else {
		upstream, err = proxy.NewOpenAIProxy(cfg.UpstreamURL, cfg.UpstreamKey, "", "", "")
	}
	if err != nil {
		log.Fatalf("configure AI upstream: %v", err)
	}
	payments := &proxy.PaymentMiddleware{Validator: solana.NewValidator(cfg.RPCURL, time.Duration(cfg.RPCTimeout)*time.Second), Cache: store.NewMemoryCache(cfg.CacheSize), WalletPubKey: cfg.Wallet, Price: cfg.Price, InputTokenPrice: cfg.InputTokenPrice, OutputTokenPrice: cfg.OutputTokenPrice}
	mux := http.NewServeMux()
	web, err := fs.Sub(webFiles, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("/v1/", payments.Wrap(upstream))
	mux.HandleFunc("GET /api/payment-info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"pay_to": cfg.Wallet, "minimum_amount": cfg.Price})
	})
	mux.HandleFunc("POST /api/quote", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, (2<<20)+1))
		if err != nil || len(body) > 2<<20 {
			http.Error(w, "Request body is too large or could not be read", http.StatusRequestEntityTooLarge)
			return
		}
		quote, err := proxy.EstimateQuote(body, cfg.InputTokenPrice, cfg.OutputTokenPrice, cfg.Price)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		quoteBody := map[string]any{
			"pay_to":          cfg.Wallet,
			"ai_provider":     primaryProvider,
			"ai_model":        primaryModel,
			"amount":          quote.Amount,
			"input_tokens":    quote.InputTokens,
			"output_tokens":   quote.OutputTokens,
			"total_tokens":    quote.TotalTokens,
			"input_amount":    quote.InputAmount,
			"output_amount":   quote.OutputAmount,
			"minimum_applied": quote.MinimumApplied,
			"estimated":       true,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(quoteBody)
	})
	mux.Handle("/", http.FileServer(http.FS(web)))
	server := &http.Server{Addr: cfg.ListenAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("PromptSOL listening on %s; RPC=%s; primary AI=%s", cfg.ListenAddr, cfg.RPCURL, primaryProvider)
	if cfg.GroqKey == "" {
		log.Printf("Groq is disabled; set GROQ_API_KEY to use it as the primary provider")
	} else {
		log.Printf("Gemini fallback is enabled; Groq model=%s", cfg.GroqModel)
	}
	log.Fatal(server.ListenAndServe())
}
