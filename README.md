# ProofPilot: Solana x402 AI Proxy Gateway

Minimal Go gateway for a fixed-price, native SOL payment on Solana Devnet. A request without a payment signature receives HTTP 402 with the recipient and price. A confirmed transfer unlocks one request to the OpenAI-compatible upstream. Responses, including SSE, stream through Go's `httputil.ReverseProxy`.

## Run

1. Copy `.env.example` values into your shell environment. Set a Devnet recipient wallet and `OPENAI_API_KEY`.
2. Start the proxy with `go run ./cmd/proxy`.
3. In another terminal, install the demo dependency with `npm install`, set `SOLANA_KEYPAIR_PATH` to a funded Devnet keypair JSON file, and run `npm run demo`.

The wallet keypair file is read locally by the demo. Fund it with Devnet SOL before running. Set `PROXY_URL` to change the endpoint.

## Payment behavior

- Only successful, confirmed System Program SOL transfers to the configured recipient qualify; the transferred amount must be at least the fixed price.
- RPC verification has a configurable timeout. RPC errors/timeouts return HTTP 503 so callers can retry; missing, failed, unconfirmed, or mismatched transactions return HTTP 402.
- A signature is claimed atomically after verification and only permits one request. A bounded in-memory cache fails closed at capacity rather than evicting signatures. Restarting the process clears it, so this is demo-grade replay protection, not durable accounting.
- A verified payment remains spent if the upstream fails; refunds and debt tracking are outside this MVP.
- Native SOL only. SPL tokens, persistence, and usage-based pricing are out of scope.

## Configuration

See `.env.example`. Required: `SOLANA_WALLET_ADDRESS`, `OPENAI_API_KEY`. The default price is 10,000 lamports. `OPENAI_BASE_URL` can point to an OpenAI-compatible service.
