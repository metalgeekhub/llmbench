# LLMBench — Project Plan

LLMBench is an AI/LLM benchmark and performance testing tool with a web UI. It ships as a single self-contained application: one binary (or one Docker container), opened in a browser. Users connect one or more LLM endpoints through the web UI or environment variables. They can then chat with any model, with every request measured, and run structured performance tests: context-length sweeps, concurrency/load tests, thinking on/off comparisons, and combinations of these. Every chat message, request, and test run is stored locally so it can be browsed, compared, and exported later.

---

## 1. Technology

**Go backend + embedded web frontend, compiled into a single binary, with SQLite for storage.**

| Layer | Choice | Reason |
|---|---|---|
| Backend | Go (1.24+) | Goroutines make it cheap to simulate hundreds of concurrent users with precise timing. Compiles to one static binary for Linux, macOS, and Windows. |
| HTTP router | `github.com/go-chi/chi/v5` | Lightweight, idiomatic, stdlib-compatible. |
| Frontend | SvelteKit with `@sveltejs/adapter-static`, TypeScript, Tailwind | Built to static files and embedded in the Go binary with `go:embed`. UI and backend are one process on one port. |
| Charts | Apache ECharts | Handles large datasets and live-updating charts well. |
| Live updates | Server-Sent Events (SSE) | Streams chat tokens and live test metrics to the browser. |
| Database | SQLite via `modernc.org/sqlite` (pure Go, no CGO) | Embedded, zero setup, one file in the data directory. |
| Tokenizer | `github.com/pkoukk/tiktoken-go`, plus optional Hugging Face tokenizer files | Builds prompts of exact token lengths; counts tokens when a server doesn't report usage. |
| Config | `gopkg.in/yaml.v3` + environment variables | Optional YAML file mirrors env var structure. |
| Packaging | GoReleaser, Docker (distroless), optional Helm chart | Downloads, containers, Kubernetes. |

The storage layer is written against a Go interface so PostgreSQL can be added later as an option for shared team instances. SQLite is the only backend for v1.

### Why not the alternatives

Rust (Axum + rust-embed) would work equally well with slightly better raw performance but slower development. Python (FastAPI) is awkward to ship as a single binary and less precise when generating heavy concurrent load. Node.js is viable but offers no advantage over Go here.

### Load generation and Kubernetes

The load generator is built into the app instead of depending on an external tool such as k6. k6 doesn't natively understand streaming LLM responses, it would break the standalone requirement, and Go's concurrency makes a native engine straightforward. An optional k6 script export is planned for teams that already use k6 in CI.

"Multiple users" covers two cases. **Simulated users** are virtual users sending concurrent requests during a load test; this is the core feature and handled by the built-in engine. **Real users** means several people sharing one LLMBench instance; this is handled by optional authentication (admin password first, OIDC later), with each run recording who started it.

For load beyond one machine, a later phase adds **agent mode**: the same binary runs as `llmbench agent` on other machines or as Kubernetes pods, the main instance distributes virtual users across them, and results are merged.

---

## 2. Architecture

```
┌─────────────────────────── llmbench (single binary) ───────────────────────────┐
│                                                                                │
│   Embedded Web UI (static SPA)  ◄──── HTTP + SSE ────►  API layer (REST)       │
│                                                         │                      │
│        ┌────────────────────┬───────────────────────────┼──────────────────┐   │
│        ▼                    ▼                           ▼                  ▼   │
│  Source manager       Chat service              Test runner /        Export /  │
│  (env + UI config,    (sessions, streaming)     load engine          import    │
│   encrypted secrets)          │                 (virtual users,      service   │
│        │                      │                  schedulers)                   │
│        └──────────┬───────────┴─────────────┬──────────┘                       │
│                   ▼                         ▼                                  │
│         Provider adapters            Metrics collector                         │
│   (OpenAI-compat, Anthropic,       (per-request timings,                       │
│    Gemini, Azure, Bedrock…)         aggregation, percentiles)                  │
│                   │                         │                                  │
│                   ▼                         ▼                                  │
│          LLM endpoints               SQLite (data dir)                         │
└────────────────────────────────────────────────────────────────────────────────┘
```

Every request, whether from the chat UI or a test, goes through the same adapter and metrics pipeline. This guarantees chat numbers and test numbers are measured identically.

---

## 3. Repository layout

```
llmbench/
├── cmd/llmbench/          # main.go — CLI entrypoint (serve, agent, version)
├── internal/
│   ├── api/               # HTTP handlers, SSE endpoints, routing
│   ├── config/            # env var + YAML parsing
│   ├── sources/           # source & model profile management
│   ├── providers/         # adapters: openai, anthropic, gemini, azure...
│   ├── chat/              # chat sessions and streaming
│   ├── metrics/           # timing capture, aggregation, percentiles
│   ├── runner/            # test definitions, load engine, schedulers
│   ├── store/             # storage interface + SQLite implementation
│   ├── export/            # JSON, CSV, HTML, Markdown exporters/importers
│   └── secrets/           # API key encryption at rest
├── migrations/            # SQL schema migrations (embedded)
├── web/                   # SvelteKit frontend (static build → embedded)
├── docs/PLAN.md           # this file
├── Makefile
├── Dockerfile
└── .goreleaser.yaml
```

### Build conventions

The frontend builds to `web/build`, which is embedded with `//go:embed` from a small Go file inside `web/` (or copied into an `internal/ui` package at build time). Because `go:embed` fails if the directory is missing, the repository keeps a placeholder `index.html` in the embed directory so `go build` works before the frontend has been built.

Makefile targets:

| Target | Action |
|---|---|
| `make dev` | Runs the Vite dev server and the Go backend (with `air`) concurrently; the Vite dev server proxies `/api` to Go |
| `make web` | Builds the frontend (`npm run build` in `web/`) |
| `make build` | `make web`, then `go build -o bin/llmbench ./cmd/llmbench` |
| `make test` | `go test ./...` and frontend checks |
| `make lint` | `golangci-lint run` |
| `make docker` | Builds the Docker image |

The API is served under `/api/v1/...`; all other paths serve the SPA with fallback to `index.html`. Database migrations are embedded and applied automatically at startup.

---

## 4. Sources (AI endpoints)

A **Source** is an endpoint connection: a type, base URL (host and port), authentication, and one or more models. A **Model Profile** is a model on a source plus default parameters (temperature, max tokens, thinking settings, system prompt, extra body). Several profiles of the same model can exist, for example "Qwen thinking-high" and "Qwen no-thinking", and they can be compared like different models.

### Adapters (priority order)

| Adapter | Covers |
|---|---|
| OpenAI-compatible | OpenAI, vLLM, SGLang, TGI, Ollama, LM Studio, llama.cpp server, LiteLLM, OpenRouter, Groq, Together, most self-hosted servers |
| Anthropic | Claude models |
| Google Gemini | Gemini models |
| Azure OpenAI | Azure deployments |
| AWS Bedrock | Later phase |

Adapters implement a common Go interface: list models, and send a streaming chat request that emits normalized events (content token, reasoning token, usage, error, done) with receive timestamps.

### Source fields

Name, type, base URL, API key (optional), custom headers (for gateways with unusual auth), TLS skip-verify, timeout, a model list (fetched automatically from `/models` where available or entered by hand), and an **extra body JSON** passthrough. The passthrough allows any provider-specific parameter without code changes.

### Thinking / reasoning mapping

The UI exposes a normalized thinking control, mapped per provider:

| Provider | Mapping |
|---|---|
| OpenAI | `reasoning_effort` (low/medium/high) |
| Anthropic | `thinking` with `budget_tokens` |
| Gemini | `thinkingConfig.thinkingBudget` |
| vLLM/SGLang (Qwen, DeepSeek, etc.) | `chat_template_kwargs.enable_thinking`, or via extra body |

### Configuration via environment variables

Sources defined through env vars appear in the UI as "managed by environment" (read-only). Sources created in the UI are stored in SQLite. API keys are encrypted at rest with a master key and never returned to the browser.

```bash
# App settings
LLMB_LISTEN=:8080
LLMB_DATA_DIR=/data
LLMB_SECRET_KEY=change-me            # encrypts stored API keys
LLMB_ADMIN_PASSWORD=optional         # enables login if set
LLMB_CONFIG_FILE=optional            # path to llmbench.yaml

# A self-hosted vLLM server
LLMB_SOURCE_LOCALVLLM_TYPE=openai
LLMB_SOURCE_LOCALVLLM_BASE_URL=http://10.0.0.5:8000/v1
LLMB_SOURCE_LOCALVLLM_MODELS=qwen3-32b,llama-3.3-70b
LLMB_SOURCE_LOCALVLLM_EXTRA_BODY={"chat_template_kwargs":{"enable_thinking":true}}

# A hosted provider
LLMB_SOURCE_OPENAI_TYPE=openai
LLMB_SOURCE_OPENAI_BASE_URL=https://api.openai.com/v1
LLMB_SOURCE_OPENAI_API_KEY=sk-...
LLMB_SOURCE_OPENAI_MODELS=auto        # fetch model list from /models

# Custom auth header for an internal gateway
LLMB_SOURCE_GATEWAY_TYPE=openai
LLMB_SOURCE_GATEWAY_BASE_URL=https://llm.internal.company/v1
LLMB_SOURCE_GATEWAY_HEADERS={"X-Api-Token":"abc123"}
```

Pattern: `LLMB_SOURCE_<NAME>_<FIELD>`, where fields are `TYPE`, `BASE_URL`, `API_KEY`, `MODELS`, `HEADERS`, `EXTRA_BODY`, `TIMEOUT`, `TLS_SKIP_VERIFY`. The optional `llmbench.yaml` has the same structure.

---

## 5. Metrics

All metrics are measured client-side with a monotonic clock, using streaming responses. Token counts come from provider-reported usage when available (for OpenAI-compatible servers, request `stream_options.include_usage`). Otherwise they are counted with a tokenizer and flagged as "estimated."

| Metric | Definition | What it tells you |
|---|---|---|
| **TTFT** (time to first token) | Request sent → first content token received | Perceived responsiveness; dominated by queueing and prefill |
| **Time to first answer token** | Request sent → first non-reasoning token | For thinking models, how long before the user sees an actual answer |
| **E2E latency** | Request sent → final token received | Total wait time |
| **TPOT** (time per output token) | (E2E − TTFT) / (output tokens − 1) | Decode speed per request |
| **ITL** (inter-token latency) | Gaps between streamed chunks, as a distribution | Streaming smoothness and stalls |
| **Output tokens/sec (per request)** | Output tokens / decode time | Generation speed one user experiences |
| **Prefill speed** | Input tokens / TTFT (approximate) | How fast the server processes long contexts |
| **Aggregate throughput** | Total output tokens across all users / wall time | Server capacity under load |
| **Request throughput** | Completed requests / second | Capacity in requests |
| **Token usage** | Input, output, reasoning, cached tokens | Load and behavior (e.g. how much reasoning a setting adds) |
| **Error rate** | Share of failed requests by type (429, 5xx, timeout, connection) | Stability and rate limits |
| **Goodput** | Share of requests meeting user-defined targets (e.g. TTFT < 1s and TPOT < 50ms) | "Useful" capacity |

Every aggregate shows min, mean, p50, p90, p95, p99, and max.

Some servers send several tokens per streamed chunk, so ITL is measured and labeled per chunk to avoid misleading numbers.

Goodput (v0.4) targets are optional per run: max TTFT, max TPOT, max E2E and min output tokens/s per request. It is evaluated on demand from the stored requests, so targets can be set, changed or previewed after a run finishes. Results show the share of requests within the SLO and good requests per second per step, and for load sweeps the highest load at which at least 90% of requests meet the targets. On Windows, measurement timestamps use the high-resolution performance counter, because the default clock only advances in ~0.5 ms steps (too coarse for TTFT and inter-chunk gaps).

---

## 6. Chat UI

A chat screen with a model selector and a parameters panel (temperature, max tokens, thinking level, system prompt, extra body). Under each response, a compact metrics strip shows TTFT, tokens/s, E2E latency, and token counts. Clicking it opens a detailed view with the full timing breakdown and a token-timeline chart. Reasoning content, when exposed by the provider, appears in a collapsible section with its own timing.

**Compare mode** sends the same prompt to two to four model profiles at once and shows responses side by side, each streaming live with its own metrics, plus a summary row highlighting the best value per metric.

**Synthetic prompts (chat and compare).** Instead of typing, the user can send a synthetic prompt of an exact token length (e.g. 4,096 tokens), optionally followed by their own instruction. This makes chat and compare usable for quick, controlled latency checks at a given context size. Synthetic prompts get a unique prefix per request so prefix caching can't favor one column over another. Long synthetic messages are shown collapsed ("Synthetic prompt · 4,096 tokens").

**Visual scoreboard (compare).** Above the turns, a scoreboard shows each column's wins per metric across all turns and its average TTFT, end-to-end latency and output speed, so the fastest model is obvious at a glance. Per-turn summaries draw inline bars next to each value, and a stacked bar per column splits output into reasoning and answer tokens.

---

## 7. Performance tests

Tests are defined in a form-based builder in the UI (also saveable and exportable as YAML/JSON), run in the background, and show a live dashboard while running.

### Test types

**Single benchmark.** N requests to one or more models with a fixed prompt and settings.

**Context-length sweep.** The same test at several input sizes (e.g. 1k, 4k, 16k, 32k, 128k tokens). Shows how TTFT and decode speed degrade as context grows. Prompts are synthesized to exact token lengths.

**Concurrency sweep.** Runs with 1, 2, 4, 8, 16, 32, … simultaneous virtual users. Shows where throughput plateaus and latency climbs, identifying the saturation point.

**Thinking comparison.** The same workload with thinking off/low/medium/high. Shows the latency and token impact of reasoning, with optional side-by-side output.

**Matrix test.** Any combination, e.g. *context [4k, 32k] × concurrency [1, 8, 32] × thinking [off, high]* = 12 cells, each with its own results. Before starting, the builder shows total request count and estimated duration.

**Ramp and soak test.** Gradually increase users over time (e.g. 1 → 64 over 10 minutes), or hold constant load for a long period to find leaks, throttling, or degradation.

### Load models

**Closed loop (fixed concurrency):** N virtual users, each sending its next request when the previous one finishes. Models "N users chatting."

**Open loop (fixed arrival rate):** requests sent at a target rate (e.g. 5 req/s with Poisson arrivals) regardless of server speed. Models real traffic and reveals queueing behavior closed-loop tests hide.

### Test options

Warm-up requests (excluded from results); requests per cell or duration per cell; output length control (`max_tokens`, plus `ignore_eos` where supported, for exact output lengths); a **cache-busting** toggle that adds a random prefix per prompt so prefix/KV caching doesn't inflate results (can be turned off to measure caching benefits); prompt source (synthetic text, fixed prompt, or imported JSONL dataset); per-request timeout; stop conditions (abort if error rate exceeds X%); and SLO targets for goodput.

### Live dashboard

Active virtual users, requests in flight, requests/sec, live TTFT and tokens/s charts, rolling error count with latest error messages, and progress through matrix cells. Tests can be stopped any time; partial results are kept.

### Results view

A summary table per cell with all metrics and percentiles, plus charts: **throughput vs. concurrency**, **TTFT vs. context length**, **latency percentiles vs. load**, **tokens/s vs. thinking level**. A per-request table supports filtering and drill-down, including failed requests.

**At a glance.** Every run opens with an overview built for a fast read:

- **Headline tiles:** peak output throughput, best TTFT, success rate (as a meter), total requests and tokens, each naming the step where it happened.
- **Automatic findings** in plain sentences, e.g. "Throughput plateaus at 8 users (716 tok/s); beyond that TTFT rises 5×", "32k context makes TTFT 11× slower than 1k", "Thinking high adds 760 ms before the answer".
- **Donut charts** for request outcomes (succeeded / failed by error type / canceled) and token composition (input, reasoning, answer).
- **Latency distribution:** histogram of TTFT or end-to-end latency across all requests, one line per model or step.
- **Heatmap** for matrix runs: two dimensions (e.g. context × users) colored by the chosen metric.

### Measurement accuracy

The load generator must never be the bottleneck. The engine monitors its own CPU usage and warns if saturated, uses a dedicated HTTP connection pool sized to the concurrency level (`MaxConnsPerHost`, `MaxIdleConnsPerHost`), and the UI notes that latency includes the network path, so server benchmarks should run close to the server.

---

## 8. Comparison

Select any set of test runs, chat requests, or model profiles and open a comparison view: side-by-side metric tables with the best value highlighted per row, overlaid charts (e.g. throughput-vs-concurrency curves for three models on one chart), and relative differences ("Model B has 38% lower p95 TTFT at 16 users"). Comparisons can be saved and exported. Imported runs can be compared too, enabling cross-machine comparisons.

The comparison view opens with **winner tiles**: for each headline metric (peak throughput, best TTFT, best end-to-end latency, error rate), which run and model is best and by how much over the runner-up. In the benchmarks list, runs are selected with checkboxes; a persistent action bar shows how many are selected and enables "Compare" from two runs on.

---

## 9. History and storage

Everything is stored in SQLite in the data directory.

| Table | Contents |
|---|---|
| `sources`, `model_profiles` | Endpoint configs (secrets encrypted) and parameter presets |
| `chat_sessions`, `chat_messages` | Full chat history |
| `requests` | **Every** request made by the app (chat or test): timings, token counts, status, error info |
| `test_definitions` | Saved, reusable, versioned test configurations |
| `test_runs`, `run_cells` | Each execution, each matrix cell, and aggregated metrics |
| `request_timelines` | Optional compressed per-chunk timestamps for detailed ITL analysis |
| `comparisons` | Saved comparison views |
| `users` | Only when authentication is enabled |

History screens support search and filter by model, date, test type, and tags; one-click re-run of past tests; and notes on runs. A retention setting can prune raw per-request data after N days while keeping aggregates.

---

## 10. Export and import

| Format | Use case |
|---|---|
| **JSON** (full fidelity) | Complete run data including config and raw requests; re-importable into another instance |
| **CSV** | Per-request raw data and per-cell summaries for Excel, pandas, etc. |
| **HTML report** | Self-contained file with charts and tables, easy to share |
| **Markdown summary** | For wikis, pull requests, documentation |
| **k6 script** | Optional, to reproduce the load in an existing k6 setup |
| **Parquet** | Later phase, for large datasets and analysis pipelines |

JSON, CSV, HTML and Markdown shipped in v0.4; k6 and Parquet remain later-phase items. Details as built:

- **JSON bundle**: `{"format": "llmbench", "version": 1, "runs": [...]}` with each run's config, steps and every request (with chunk timelines). Exported from a run, from a selection in the Benchmarks list, or from a comparison. Importing skips runs already present (same ID) and marks the others as imported; a run that was still in progress when exported is imported as interrupted.
- **CSV**: one row per request (with an `slo_met` column when the run has SLO targets), or one row per step with all percentiles and goodput.
- **HTML / Markdown**: generated in the browser from the same data and automatic findings as the run and comparison pages; the HTML file is self-contained, with charts as inline SVG.
- **Test definitions** export as YAML or JSON (`kind: llmbench-definition`) and import from the same Import button as run bundles.

A **Prometheus `/metrics` endpoint** is planned so live test metrics can feed existing Grafana dashboards.

---

## 11. Deployment

The same binary runs anywhere:

```bash
# Direct
./llmbench serve            # then open http://localhost:8080

# Docker
docker run -p 8080:8080 -v llmbench-data:/data \
  -e LLMB_SOURCE_LOCAL_TYPE=openai \
  -e LLMB_SOURCE_LOCAL_BASE_URL=http://host.docker.internal:8000/v1 \
  llmbench
```

Kubernetes: a small Helm chart (Deployment + PersistentVolumeClaim for the SQLite file). A later phase adds agent pods for distributed load generation.

---

## 12. Roadmap

| Phase | Scope |
|---|---|
| **MVP** | Single binary, SQLite, env + UI source config, OpenAI-compatible adapter, chat with per-message metrics, request history |
| **v0.2** | Anthropic and Gemini adapters, thinking controls, side-by-side chat compare, single benchmark and concurrency sweep, live dashboard |
| **v0.3** | Context sweep, thinking comparison, matrix tests, open-loop load model, results charts, run comparison view |
| **v0.3.5** | Visual insights: run "at a glance" overview (headline tiles, automatic findings, outcome and token donuts, latency histogram, matrix heatmap), comparison winner tiles, compare scoreboard with inline bars, synthetic exact-length prompts in chat and compare, visible run-selection action bar |
| **v0.4** | Exports (JSON, CSV, HTML, Markdown), import, saved test definitions, goodput/SLOs |
| **v1.0** | Authentication (password, then OIDC), dataset import, ramp/soak tests, Prometheus endpoint, Helm chart |
| **Later** | Agent mode for distributed load, Bedrock adapter, k6 export, scheduled runs, optional PostgreSQL backend |

### MVP definition of done

The MVP is complete when all of the following work: `make build` produces a single binary that serves the UI and API on one port; a source defined via env vars appears in the UI as read-only, and a source can be added, edited, and deleted in the UI with its API key encrypted in SQLite; models are listed from `/models` or entered manually; a user can chat with a model with streamed responses; each response shows TTFT, E2E latency, TPOT, output tokens/s, and token counts; every request is persisted with its metrics; chat history survives a restart; and unit tests cover the metrics calculations (TTFT, TPOT, percentiles) and env var parsing.

---

## 13. Existing tools, for reference

Open-source projects with partial overlap, useful for metric definitions and validating results: vLLM `benchmark_serving`, NVIDIA GenAI-Perf, GuideLLM, and LLMPerf. Most are CLI-only, focused on one server type, or lack persistent history and a chat UI. LLMBench's differentiators are the combined chat + benchmarking UI, multi-provider comparison, full history, and single-binary simplicity.