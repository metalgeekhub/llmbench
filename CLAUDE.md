# LLMBench

AI/LLM benchmark and performance testing tool with a web UI, shipped as one self-contained binary (or one Docker container). The full design lives in [docs/PLAN.md](docs/PLAN.md); read it before starting any feature work.

## Scope discipline

The roadmap is phased (PLAN.md §12). **Only implement the phase you were asked for.** The current phase is **MVP**: single binary, SQLite, env + UI source config, OpenAI-compatible adapter, chat with per-message metrics, request history. Do not add Anthropic/Gemini adapters, thinking controls, compare mode, load tests, exports, or auth until their phase.

## Stack

| Layer | Choice |
|---|---|
| Backend | Go (module `github.com/metalgeekhub/llmbench`) |
| Router | `github.com/go-chi/chi/v5` |
| Database | SQLite via `modernc.org/sqlite` (pure Go, **no CGO**) |
| Tokenizer | `github.com/pkoukk/tiktoken-go` with the embedded offline loader (`tiktoken-go-loader`); never fetch BPE files at runtime |
| Config | env vars + optional YAML (`gopkg.in/yaml.v3`) |
| Frontend | SvelteKit + `adapter-static`, TypeScript, Svelte 5 runes, Tailwind v4 |
| Charts | Apache ECharts |
| Live updates | Server-Sent Events |

## Repository layout

```
cmd/llmbench/        CLI entrypoint (serve, version; agent is a later phase)
internal/api/        HTTP handlers, SSE, routing, SPA serving
internal/config/     env var + YAML parsing
internal/sources/    source management (env + DB), provider construction
internal/providers/  adapters behind a common interface (openai first)
internal/chat/       chat sessions and streaming
internal/metrics/    timing capture, metric computation, percentiles, Collect()
internal/tokenizer/  token counting for estimated usage
internal/store/      storage interface + SQLite implementation
internal/secrets/    API key encryption at rest (AES-256-GCM)
internal/ui/         go:embed of the built frontend (dist/ is a build artifact)
migrations/          SQL migrations, embedded and auto-applied at startup
web/                 SvelteKit frontend
```

## Build and run

| Command | Action |
|---|---|
| `make dev` | Vite dev server + Go backend (`air`) concurrently; Vite proxies `/api` to `:8080` |
| `make web` | `npm run build` in `web/`, then copies `web/build` into `internal/ui/dist` |
| `make build` | `make web`, then `go build -o bin/llmbench ./cmd/llmbench` |
| `make test` | `go test ./...` and `npm run check` |
| `make lint` | `golangci-lint run` |
| `make docker` | Builds the Docker image |

On Windows, run `make` from Git Bash (recipes use POSIX `find`/`cp`); the Makefile adds `.exe` to binaries and `make dev` uses `.air.windows.toml`.

Without `make`: `cd web && npm run build`, copy `web/build/*` to `internal/ui/dist/`, then `go build -o bin/llmbench ./cmd/llmbench`.

`internal/ui/dist/` is gitignored except for `.gitkeep`, so `go build` works before the frontend is built; in that case the server shows a built-in placeholder page.

## Architecture rules

- **One measurement pipeline.** Every LLM request (chat now, tests later) goes through a provider adapter and `metrics.Collect`, and is persisted as a row in `requests`. Never measure timings anywhere else, so chat and test numbers are always comparable.
- **Adapters** implement `providers.Provider`: list models, and stream a chat request emitting normalized events (content, reasoning, usage, done) stamped with receive time. Provider-specific parameters go through the **extra body** passthrough (deep-merged: source extra body, then request extra body), not new code paths.
- **Storage** is only accessed through the `store.Store` interface so PostgreSQL can be added later. SQLite is the only backend for v1.
- **Migrations** are numbered SQL files in `migrations/` (`NNNN_name.sql`), applied in order at startup and recorded in `schema_migrations`. Never edit an applied migration; add a new one.
- **Timestamps** are stored as Unix milliseconds (INTEGER). IDs are UUIDs, except env sources whose ID is `env-<name>`.

## API

- REST under `/api/v1/...`; every other path serves the SPA with fallback to `index.html`.
- JSON is `snake_case`. Errors are `{"error": "message"}` with a proper HTTP status.
- Streaming endpoints use SSE (`text/event-stream`). Chat streaming is a `POST`, so the frontend reads it with `fetch` + a stream parser, not `EventSource`.

## Sources and secrets

- A **Source** = type, base URL, optional API key, custom headers, TLS skip-verify, timeout, model list (manual or `auto` = fetched from `/models`), extra body JSON.
- Env-defined sources: `LLMB_SOURCE_<NAME>_<FIELD>` with fields `TYPE`, `BASE_URL`, `API_KEY`, `MODELS`, `HEADERS`, `EXTRA_BODY`, `TIMEOUT`, `TLS_SKIP_VERIFY`. They are **read-only** in the UI ("managed by environment"). `<NAME>` may contain underscores; fields are matched by suffix.
- App settings: `LLMB_LISTEN` (default `:8080`), `LLMB_DATA_DIR` (default `./data`), `LLMB_SECRET_KEY`, `LLMB_ADMIN_PASSWORD` (auth is v1.0, parsed but unused), `LLMB_CONFIG_FILE` (YAML with the same structure), `LLMB_DEBUG` (any value enables debug logging). Precedence: defaults < YAML < env.
- API keys and custom headers are encrypted at rest with a key derived from `LLMB_SECRET_KEY` (if unset, a random key is generated in `<data_dir>/secret.key`). They are **write-only**: the API never returns them, only `has_api_key` / `header_names`. On update, an omitted (`null`) key or headers field keeps the stored value.

## Metrics (PLAN.md §5)

- Measured client-side with Go's monotonic clock on streaming responses. Start = immediately before the request is sent.
- Token counts come from provider-reported usage (OpenAI-compatible: always send `stream_options.include_usage`). If missing, count with the tokenizer and set `tokens_estimated = true`.
- Definitions:
  - **TTFT**: start → first streamed token of any kind (reasoning or answer).
  - **Time to first answer token**: start → first non-reasoning token.
  - **E2E**: start → final chunk received.
  - **TPOT**: (E2E − TTFT) / (output tokens − 1); undefined when output tokens ≤ 1.
  - **Output tokens/s**: output tokens / (E2E − TTFT).
  - **Prefill speed**: input tokens / TTFT.
  - **ITL**: gaps between token-bearing chunks, labeled **per chunk** (servers may batch tokens).
- Aggregates always report min, mean, p50, p90, p95, p99, max. Percentiles use linear interpolation between closest ranks.
- Undefined metrics are `null` in JSON, never `0`.

## Testing

- Unit tests are required for metric calculations (TTFT, TPOT, percentiles), env var / YAML parsing, and adapter stream parsing. Use `httptest` fake servers for providers; never call real LLM endpoints in tests.
- Run `go test ./...` and `npm run check` (in `web/`) before considering work done.

## Frontend conventions

- Svelte 5 runes only (`$state`, `$derived`, `$props`, `$effect`); no legacy stores for component state.
- All backend calls go through `web/src/lib/api.ts` with typed responses mirroring the Go JSON.
- Tailwind utility classes; no component library. Keep the SPA static (`ssr = false`, `adapter-static` with `fallback: 'index.html'`).
