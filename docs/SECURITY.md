# Security notes

This repository is an MVP intended for local evaluation on Solana Devnet. Do not use it to accept mainnet funds without a security review and production payment design.

## Secrets and wallets

- Keep `.env`, wallet keypairs, API keys, and RPC credentials out of Git.
- Use a dedicated Devnet recipient and test wallet for demos.
- Do not enable the optional demo client with a wallet containing funds you cannot lose.
- The browser never receives the Gemini or Groq API key; provider requests are made by the proxy.

## Payment verification

- The proxy verifies transactions through the configured Solana RPC before forwarding a paid request.
- It checks the destination, minimum lamport amount, and transaction result.
- A signature can be claimed only once within the running process.
- The in-memory signature cache is not persistent and is not suitable for multiple server replicas.

## Known MVP limitations

- The interface uses Devnet and test SOL.
- Payment signatures and retry state are not backed by persistent storage.
- A successful transfer is not refunded if the user abandons the answer or the process stops.
- The quote uses an estimate and reserves the selected maximum answer allowance.
- The payment header is custom; the current app is not a full x402 protocol implementation.

## Reporting an issue

Please do not publish secrets, wallet seed phrases, signed transactions containing real funds, or private API keys in an issue. Describe the affected route, expected behavior, and sanitized logs instead.
