# AGENTS.md

Fork of [router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI):
a Go 1.26+ proxy server providing OpenAI/Gemini/Claude/Codex compatible APIs
with OAuth and load balancing across credentials. Project intent and planning
live in the sibling annexe repo (`../CLIProxyAPI-Annexe`;
`BFJ-Concerns/CLIProxyAPI-Annexe` on Forgejo) — its `README.md` is the
contract for what this fork is.

## Fork discipline

- `main` mirrors upstream exactly — never commit to it. All fork work lands
  on `patches`, which is rebased onto upstream releases.
- The fork is a minimal additive patch. Touch upstream code only at the seams
  a change genuinely needs; put new behaviour in new files; integrate through
  upstream's own interfaces rather than copying upstream logic into fork
  files.
- Never reformat, refactor, translate comments in, or otherwise "improve"
  upstream code a change did not need to touch. Upstream code is not brought
  to any house standard — every gratuitous divergence is a future rebase
  conflict. This deliberately overrides any convention, here or in other
  loaded guidance, that would have you clean up code you pass through.

## Commands

```bash
gofmt -w .                                        # Format (required after Go changes)
go build -o cli-proxy-api ./cmd/server            # Build
go run ./cmd/server                               # Run dev server
go test ./...                                     # Run all tests
go test -v -run TestName ./path/to/pkg            # Run single test
go build -o test-output ./cmd/server && rm test-output  # Verify compile (REQUIRED after changes)
```

- Common flags: `--config <path>`, `--tui`, `--standalone`, `--local-model`, `--no-browser`, `--oauth-callback-port <port>`

## Config

- Default config: `config.yaml` (template: `config.example.yaml`)
- `.env` is auto-loaded from the working directory
- Auth material defaults under `auths/`
- Storage backends: file-based default; optional Postgres/git/object store (`PGSTORE_*`, `GITSTORE_*`, `OBJECTSTORE_*`)

## Architecture

- `cmd/server/` — Server entrypoint
- `internal/api/` — Gin HTTP API (routes, middleware, modules)
- `internal/thinking/` — Thinking/reasoning pipeline: suffix parsing →
  canonical `ThinkingConfig` → central validation → per-provider output via
  `ProviderApplier`. Do not break the "canonical representation →
  per-provider translation" architecture.
- `internal/runtime/executor/` — Per-provider runtime executors (incl. Codex
  WebSocket). Executors and their unit tests only; helper files go under
  `internal/runtime/executor/helps/`.
- `internal/translator/` — Provider protocol translators
- `internal/registry/` — Model registry + remote updater; `--local-model`
  disables remote updates
- `internal/store/` — Storage implementations and secret resolution
- `internal/watcher/` — Config hot-reload
- `internal/usage/` — Usage and token accounting
- `internal/home/` — CLIProxyAPIHome control plane integration (bootstrap, RESP communication, dispatch coordination)
- `internal/tui/` — Bubbletea terminal UI (`--tui`, `--standalone`)
- `sdk/cliproxy/` — Embeddable SDK entry (service/builder/watchers/pipeline);
  credential selection strategies live in `sdk/cliproxy/auth/`
- `test/` — Cross-module integration tests

## Upstream law

Timeouts are allowed only during credential acquisition; once an upstream
connection is established, no timeouts on any subsequent network behaviour.
Intentional exceptions: the Codex websocket liveness deadlines
(`internal/runtime/executor/codex_websockets_executor.go`), the wsrelay
session deadlines (`internal/wsrelay/session.go`), the management APICall
timeout (`internal/api/handlers/management/api_tools.go`), and the
`cmd/fetch_antigravity_models` utility timeouts.

Payload configuration is the final semantic barrier before every executor
sends a request — streaming, WebSocket, continuation, retry/fallback, image,
and token-count paths included. Finish all built-in translation,
normalisation, injection, and cleanup first, then apply user payload rules
exactly once; nothing afterwards may overwrite configured values or restore
filtered fields. Add regression tests when changing request-building paths.

Fork code follows upstream's Go idiom so it reads like the code around it:
English comments; logrus structured logging that never leaks secrets or
tokens; no `log.Fatal` and no panics in HTTP handlers; shadowed errors named
for their call (`errStart := server.Start()`); close and cleanup errors
logged rather than discarded.

Unit tests for TTL, expiration, ordering, or cache eviction must not rely on
wall-clock `time.Sleep` (platform timer granularity and CI jitter make them
flaky); use controllable clocks (`nowFunc` / mock clock), explicit timestamp
manipulation, or deterministic synchronisation primitives.

Features that touch CLIProxyAPIHome (`internal/home/`) may need a matching
change in the CLIProxyAPIHome repository; check before landing.

Endpoints under `/v0/management` are deprecated upstream and no longer
maintained; build management features on `/v8/management`, and touch `/v0`
endpoints only where compilation requires it.
