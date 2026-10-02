package config

import (
	"errors"
	"os"
	"strconv"
)

type Config struct {
	ListenAddr string
	RPCURL     string
	OpenAIURL  string
	OpenAIKey  string
	Wallet     string
	Price      uint64
	RPCTimeout int
	CacheSize  int
}

func Load() (Config, error) {
	c := Config{
		ListenAddr: env("LISTEN_ADDR", ":8080"),
		RPCURL:     env("SOLANA_RPC_URL", "https://api.devnet.solana.com"),
		OpenAIURL:  env("OPENAI_BASE_URL", "https://api.openai.com"),
		OpenAIKey:  os.Getenv("OPENAI_API_KEY"),
		Wallet:     os.Getenv("SOLANA_WALLET_ADDRESS"),
		Price:      10000,
		RPCTimeout: 5,
		CacheSize:  10000,
	}
	if s := os.Getenv("PAYMENT_LAMPORTS"); s != "" {
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil || v == 0 {
			return Config{}, errors.New("PAYMENT_LAMPORTS must be a positive integer")
		}
		c.Price = v
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
	if c.OpenAIKey == "" {
		return Config{}, errors.New("OPENAI_API_KEY is required")
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
