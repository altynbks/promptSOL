# ProofPilot: Solana x402 AI Proxy Gateway

Minimal Go gateway for a fixed-price, native SOL payment on Solana Devnet. A request without a payment signature receives HTTP 402 with the recipient and price. A confirmed transfer unlocks one request to an OpenAI-compatible upstream such as Gemini. Responses, including SSE, stream through Go's `httputil.ReverseProxy`.

## Run with Docker

1. Copy `.env.example` to `.env` and set `SOLANA_WALLET_ADDRESS` to the recipient wallet and `UPSTREAM_API_KEY` to your Gemini API key from Google AI Studio.
2. Start the proxy:

   ```sh
   docker compose up --build -d proxy
   ```

   Open `http://localhost:8080` for the demo interface. Connect Phantom on Solana Devnet, enter a prompt, approve the SOL transfer, and the Gemini response will stream into the page. Each successful request consumes one payment.

The optional terminal demo is also available. Put a funded Devnet keypair JSON file at `wallet.json` in the project directory, then run:

```sh
docker compose --profile demo run --build --rm demo
```

The demo container mounts `wallet.json` read-only and sends requests to Gemini using the OpenAI compatible endpoint. `UPSTREAM_BASE_URL` points to that endpoint by default, and `AI_MODEL` defaults to `gemini-3.8-flash`. You can change `PROXY_URL` or `SOLANA_RPC_URL` in `.env`. The proxy and demo use the Devnet RPC by default.

Stop the proxy with `docker compose down`.

## Payment behavior

- Only successful, confirmed System Program SOL transfers to the configured recipient qualify; the transferred amount must be at least the fixed price.
- RPC verification has a configurable timeout. RPC errors/timeouts return HTTP 503 so callers can retry; missing, failed, unconfirmed, or mismatched transactions return HTTP 402.
- A signature is claimed atomically after verification and only permits one request. A bounded in-memory cache fails closed at capacity rather than evicting signatures. Restarting the process clears it, so this is demo-grade replay protection, not durable accounting.
- A verified payment remains spent if the upstream fails; refunds and debt tracking are outside this MVP.
- Native SOL only. SPL tokens, persistence, and usage-based pricing are out of scope.

## Configuration

See `.env.example`. Required: `SOLANA_WALLET_ADDRESS`, `UPSTREAM_API_KEY`. The default upstream is Gemini's OpenAI-compatible API; `UPSTREAM_BASE_URL` can point to another compatible service. The default price is 10,000 lamports.
