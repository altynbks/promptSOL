package config

import (
	"errors"
	"os"
	"strconv"
)

type Config struct {
	ListenAddr       string
	RPCURL           string
	UpstreamURL      string
	UpstreamKey      string
	AIModel          string
	Wallet           string
	LamportsPerToken uint64
	RPCTimeout       int
	CacheSize        int
}

const devnetRPCURL = "https://api.devnet.solana.com"

func Load() (Config, error) {
	c := Config{
		ListenAddr:       env("LISTEN_ADDR", ":8080"),
		RPCURL:           env("SOLANA_RPC_URL", devnetRPCURL),
		UpstreamURL:      env("UPSTREAM_BASE_URL", "https://generativelanguage.googleapis.com/v1beta/openai/"),
		UpstreamKey:      os.Getenv("UPSTREAM_API_KEY"),
		AIModel:          env("AI_MODEL", "gemini-3.8-flash"),
		Wallet:           os.Getenv("SOLANA_WALLET_ADDRESS"),
		LamportsPerToken: 10,
		RPCTimeout:       5,
		CacheSize:        10000,
	}
	if c.RPCURL != devnetRPCURL {
		return Config{}, errors.New("SOLANA_RPC_URL must be the Solana Devnet RPC for this demo")
	}
	if s := os.Getenv("PAYMENT_PER_TOKEN_LAMPORTS"); s != "" {
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil || v == 0 {
			return Config{}, errors.New("PAYMENT_PER_TOKEN_LAMPORTS must be a positive integer")
		}
		c.LamportsPerToken = v
	}
	if s := os.Getenv("RPC_TIMEOUT_SECONDS"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v < 1 {
			return Config{}, errors.New("RPC_TIMEOUT_SECONDS must be positive")
		}
		c.RPCTimeout = v
	}
	if s := os.Getenv("SIGNATURE_CACHE_SIZE"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v < 1 {
			return Config{}, errors.New("SIGNATURE_CACHE_SIZE must be positive")
		}
		c.CacheSize = v
	}
	if c.UpstreamKey == "" {
		return Config{}, errors.New("UPSTREAM_API_KEY is required")
	}
	if c.Wallet == "" {
		return Config{}, errors.New("SOLANA_WALLET_ADDRESS is required")
	}
	return c, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
