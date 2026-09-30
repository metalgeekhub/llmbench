# LLMBench

AI/LLM benchmark and performance testing tool with a web UI, shipped as one self-contained binary (or one Docker container). The full design lives in [docs/PLAN.md](docs/PLAN.md); read it before starting any feature work.

## Scope discipline

The roadmap is phased (PLAN.md §12). **Only implement the phase you were asked for.** Done so far:

- **MVP**; **v0.2**: benchmark engine, thinking controls, model profiles, side-by-side chat compare, live dashboard. The Anthropic and Gemini adapters were **deferred by the user**; don't add them unless asked.
- **v0.3**: context sweep, thinking comparison, matrix tests, open-loop load model, results chart explorer, run comparison view (saved comparisons).
- **v0.3.5** (added by the user, see PLAN.md §12): visual insights — run "at a glance" overview, comparison winner tiles, compare scoreboard, synthetic exact-length prompts in chat/compare, visible run-selection action bar.
- **v0.4**: exports (JSON bundle, CSV, HTML report, Markdown), import of run bundles and definition files, saved test definitions with versions, goodput/SLOs.

Cost estimation was **dropped from the roadmap by the user**; do not add it.

Do not add v1.0 features (auth, dataset import, ramp/soak, Prometheus, Helm) or "Later" items (k6 export, Parquet, agents) until their phase.

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
internal/metrics/    timing capture, metric computation, percentiles, Collect(), cell Aggregate, SLO/goodput
internal/clock/      high-resolution timestamps for measurements
internal/runner/     benchmark engine: config validation, closed-loop virtual users, runs/cells, live series, goodput
internal/profiles/   model profiles (a model plus saved parameters)
internal/definitions/ saved, versioned test definitions; YAML/JSON definition files
internal/export/     run export bundle (JSON) and import, CSV exports
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
- **Benchmark matrix**: a run is a list of cells, one per target × context length × thinking level × load level (users for the closed loop, requests/s for the open loop), ordered target → context → thinking → load, with load innermost. Test types (`single`, `concurrency_sweep`, `context_sweep`, `thinking_comparison`, `matrix`) are presets that constrain which dimensions vary; at most 256 cells. Context sweeps always use synthetic prompts of exact cl100k lengths. A thinking sweep overrides each target's thinking level; the style comes from the target's profile if set, else from the run.
- **Open loop**: Poisson arrivals at the cell's rate, independent of response times, capped at `max_in_flight` concurrent requests. Arrivals that must wait for a slot are recorded as schedule lag (`schedule_lag_p95_ms`); latency is still measured from the actual send.
- **Benchmark engine** (`internal/runner`): cells are executed in order. Only one run executes at a time (`ErrBusy`) so benchmarks don't distort each other. Each cell gets a dedicated provider whose connection pool is sized to its concurrency (`sources.LoadProvider`). Virtual users never write to SQLite themselves: results go through a single batching writer. Warm-up requests are stored with `warmup = 1` and excluded from summaries; cell latency summaries include successful requests only. Runs left `running` by a crashed process are marked `interrupted` at startup.
- **Thinking control**: one normalized level (`""` = server default, `off`, `low`, `medium`, `high`) plus a style saying how to send it: `reasoning_effort` (OpenAI, gpt-oss; `off` → `"none"`) or `chat_template_kwargs` (`enable_thinking`, on/off only). The mapping lives in the adapter (`providers.ChatRequest.Thinking`) and overrides any reasoning settings in the extra bodies. `store.ChatParams` (used by chat, profiles, benchmarks) validates it.
- **Model profiles** are chosen in chat/compare by copying their settings into the session (`profile_id` records the origin). As benchmark targets they are resolved at start: the run's own max tokens, temperature and system prompt win where set; the run's extra body is merged over the profile's.
- **Compare** = chat sessions sharing a `compare_id`, one per column, each an ordinary measured chat. Plain chat lists exclude them.
- **Live dashboard**: the runner samples per-second points (`LivePoint`) in memory, streams them via `GET /api/v1/runs/{id}/events` (SSE, incremental), and saves the series to `test_runs.timeline` when the run ends.
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

- Measured client-side on streaming responses. Start = immediately before the request is sent. Measurement timestamps come from `clock.Now()` (`internal/clock`), never `time.Now()`: on Windows `time.Now()` only advances in ~0.5 ms steps, so `clock` reads the high-resolution performance counter there.
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
- **Goodput** is evaluated on demand from stored requests (`metrics.SLO`, `runner.Goodput`), never stored, so targets can be changed or previewed ("what-if") after a run. A request is good when it succeeded and meets every set target; warm-up and canceled requests are excluded. The UI counts a step as within the SLO when ≥90% of its requests are good.

## Definitions, export and import

- Saved test definitions are versioned: every save of a changed config is a new version (`test_definition_versions`); runs record `definition_id` / `definition_version`. Definition files (YAML or JSON) have `kind: llmbench-definition`; importing a name that exists adds a version.
- The run export bundle is JSON with `"format": "llmbench"` and a `version` (currently 1); bump it on incompatible changes and keep reading older versions. Import skips runs whose ID already exists and sets `imported_at`.
- CSV exports come from the server; HTML and Markdown reports are generated in the browser (`web/src/lib/report.ts`, charts via ECharts SSR to inline SVG) from the same findings the pages show.

## Testing

- Unit tests are required for metric calculations (TTFT, TPOT, percentiles), env var / YAML parsing, and adapter stream parsing. Use `httptest` fake servers for providers; never call real LLM endpoints in tests.
- Run `go test ./...` and `npm run check` (in `web/`) before considering work done.

## Frontend conventions

- Svelte 5 runes only (`$state`, `$derived`, `$props`, `$effect`); no legacy stores for component state.
- All backend calls go through `web/src/lib/api.ts` with typed responses mirroring the Go JSON.
- Tailwind utility classes; no component library. Keep the SPA static (`ssr = false`, `adapter-static` with `fallback: 'index.html'`).
