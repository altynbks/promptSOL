package config

import "testing"

func TestLoadKeepsDemoOnDevnetAndReadsTokenRate(t *testing.T) {
	t.Setenv("SOLANA_RPC_URL", devnetRPCURL)
	t.Setenv("SOLANA_WALLET_ADDRESS", "public-recipient")
	t.Setenv("UPSTREAM_API_KEY", "test-only")
	t.Setenv("AI_MODEL", "gemini-test-model")
	t.Setenv("PAYMENT_PER_TOKEN_LAMPORTS", "25")
	config, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.RPCURL != devnetRPCURL || config.LamportsPerToken != 25 || config.AIModel != "gemini-test-model" {
		t.Fatalf("unexpected demo configuration: RPC=%q rate=%d model=%q", config.RPCURL, config.LamportsPerToken, config.AIModel)
	}
}

func TestLoadRejectsNonDevnetRPC(t *testing.T) {
	t.Setenv("SOLANA_RPC_URL", "https://api.mainnet-beta.solana.com")
	t.Setenv("SOLANA_WALLET_ADDRESS", "public-recipient")
	t.Setenv("UPSTREAM_API_KEY", "test-only")
	if _, err := Load(); err == nil {
		t.Fatal("mainnet RPC must be rejected by the demo configuration")
	}
}
