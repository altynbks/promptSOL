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
	GroqURL          string
	GroqKey          string
	GroqModel        string
	GeminiModel      string
	Wallet           string
	Price            uint64
	InputTokenPrice  uint64
	OutputTokenPrice uint64
	RPCTimeout       int
	CacheSize        int
}

func Load() (Config, error) {
	c := Config{
		ListenAddr:       env("LISTEN_ADDR", ":8080"),
		RPCURL:           env("SOLANA_RPC_URL", "https://api.devnet.solana.com"),
		UpstreamURL:      env("UPSTREAM_BASE_URL", "https://generativelanguage.googleapis.com/v1beta/openai/"),
		UpstreamKey:      os.Getenv("UPSTREAM_API_KEY"),
		GroqURL:          env("GROQ_BASE_URL", "https://api.groq.com/openai"),
		GroqKey:          os.Getenv("GROQ_API_KEY"),
		GroqModel:        env("GROQ_MODEL", "openai/gpt-oss-20b"),
		GeminiModel:      env("GEMINI_MODEL", "gemini-3.8-flash"),
		Wallet:           os.Getenv("SOLANA_WALLET_ADDRESS"),
		Price:            1000,
		InputTokenPrice:  2000,
		OutputTokenPrice: 8000,
		RPCTimeout:       20,
		CacheSize:        10000,
	}
	if s := os.Getenv("PAYMENT_LAMPORTS"); s != "" {
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil || v == 0 {
			return Config{}, errors.New("PAYMENT_LAMPORTS must be a positive integer")
		}
		c.Price = v
	}
	if s := os.Getenv("INPUT_LAMPORTS_PER_1K_TOKENS"); s != "" {
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil || v == 0 {
			return Config{}, errors.New("INPUT_LAMPORTS_PER_1K_TOKENS must be a positive integer")
		}
		c.InputTokenPrice = v
	}
	if s := os.Getenv("OUTPUT_LAMPORTS_PER_1K_TOKENS"); s != "" {
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil || v == 0 {
			return Config{}, errors.New("OUTPUT_LAMPORTS_PER_1K_TOKENS must be a positive integer")
		}
		c.OutputTokenPrice = v
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
	if !validSolanaAddress(c.Wallet) {
		return Config{}, errors.New("SOLANA_WALLET_ADDRESS must be a valid 32-byte Solana public key in base58 (check the value in .env)")
	}
	return c, nil
}

func validSolanaAddress(value string) bool {
	const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	// Decode base58 into a little-endian byte array. Public keys must decode to
	// exactly 32 bytes; this also rejects whitespace and other invalid characters.
	decoded := make([]byte, 0, 32)
	for _, char := range value {
		carry := -1
		for i, candidate := range alphabet {
			if char == candidate {
				carry = i
				break
			}
		}
		if carry < 0 {
			return false
		}
		for i := range decoded {
			carry += int(decoded[i]) * 58
			decoded[i] = byte(carry)
			carry >>= 8
		}
		for carry > 0 {
			decoded = append(decoded, byte(carry))
			carry >>= 8
		}
	}
	leadingZeros := 0
	for _, char := range value {
		if char != '1' {
			break
		}
		leadingZeros++
	}
	return leadingZeros+len(decoded) == 32
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
