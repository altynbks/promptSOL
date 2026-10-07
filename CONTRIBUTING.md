# Contributing

Thanks for helping improve PromptSOL. Keep changes focused, document user-visible behavior, and avoid committing credentials or wallet material.

## Before opening a pull request

Run the available checks from the repository root:

```sh
gofmt -w cmd/proxy/main.go internal/config/*.go internal/proxy/*.go internal/solana/*.go internal/store/*.go
go test ./...
go vet ./...
node --check cmd/proxy/web/app.js
docker compose build proxy
```

The Go packages currently compile but do not yet have dedicated unit-test files. Please include focused tests when changing payment verification, quote calculation, retry behavior, or provider routing.

## Pull request notes

Include:

- what changed and why;
- how the change was checked;
- screenshots for visible interface changes when practical;
- any new environment variables or Devnet setup steps.

Never include `.env`, API keys, seed phrases, wallet JSON files, or real payment signatures.
