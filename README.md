# LLMBench

A benchmark and performance testing tool for LLM endpoints, with a web UI. It ships as one self-contained binary (or one Docker container): start it, open a browser, point it at your models.

- **Chat** with any model and see the metrics of every message: time to first token, output speed, token counts.
- **Compare** models side by side on the same prompt, including synthetic prompts of an exact token length.
- **Benchmark** under load: concurrency and request-rate sweeps, context-length sweeps, thinking on/off comparisons, or any combination (a matrix), with a live dashboard while it runs.
- **Understand results quickly**: headline tiles, automatic findings ("throughput plateaus at 8 users"), charts, histograms and heatmaps, and run-vs-run comparisons.
- **Goodput / SLOs**: set latency targets (e.g. TTFT ≤ 500 ms, TPOT ≤ 50 ms) and see how many requests meet them, and the highest load that still does. Targets can be changed after a run.
- **Keep and share**: every request is stored locally (SQLite). Save test definitions, export runs as JSON, CSV, an HTML report or Markdown, and import runs from another instance.

Any **OpenAI-compatible** endpoint works: OpenAI, vLLM, SGLang, TGI, Ollama, LM Studio, llama.cpp server, LiteLLM, OpenRouter, and others.

## Quick start

### Binary

Build it (see [Building](#building)), then:

```bash
./bin/llmbench serve
```

Open <http://localhost:8080>, go to **Sources** and add an endpoint, or define one with environment variables:

```bash
LLMB_SOURCE_LOCAL_TYPE=openai \
LLMB_SOURCE_LOCAL_BASE_URL=http://localhost:8000/v1 \
./bin/llmbench serve
```

### Docker

```bash
docker build -t llmbench .
docker run -p 8080:8080 -v llmbench-data:/data \
  -e LLMB_SOURCE_LOCAL_TYPE=openai \
  -e LLMB_SOURCE_LOCAL_BASE_URL=http://host.docker.internal:8000/v1 \
  llmbench
```

Data (the SQLite database and the generated encryption key) lives in `/data`; keep it on a volume.

## Configuration

Settings come from defaults, then an optional YAML file, then environment variables (highest precedence).

| Variable | Default | Meaning |
|---|---|---|
| `LLMB_LISTEN` | `:8080` | Listen address |
| `LLMB_DATA_DIR` | `./data` | Database and key directory |
| `LLMB_SECRET_KEY` | generated | Key used to encrypt stored API keys and headers. If unset, a random key is created in `<data_dir>/secret.key` |
| `LLMB_CONFIG_FILE` | none | Path to a YAML config file |
| `LLMB_DEBUG` | off | Any value enables debug logging |

### Sources from the environment

A source is one endpoint. Define it with `LLMB_SOURCE_<NAME>_<FIELD>`; `<NAME>` may contain underscores.

| Field | Example | Notes |
|---|---|---|
| `TYPE` | `openai` | Required |
| `BASE_URL` | `http://gpu-box:8000/v1` | Required |
| `API_KEY` | `sk-...` | Optional |
| `MODELS` | `auto` or `llama-3-8b,qwen3-32b` | `auto` (the default) lists models from the endpoint's `/models` |
| `HEADERS` | `{"X-Team":"perf"}` | JSON object |
| `EXTRA_BODY` | `{"top_k":20}` | JSON object merged into every request |
| `TIMEOUT` | `120` or `2m` | Seconds or a duration |
| `TLS_SKIP_VERIFY` | `true` | For self-signed certificates |

Sources defined in the environment are read-only in the UI. Sources added in the UI are stored in the database; API keys and headers are encrypted at rest and never returned by the API.

### YAML

```yaml
listen: ":8080"
data_dir: ./data
sources:
  gpu_box:
    type: openai
    base_url: http://gpu-box:8000/v1
    models: auto
    timeout: 2m
  openai:
    type: openai
    base_url: https://api.openai.com/v1
    api_key: sk-...
    models: [gpt-4o-mini]
```

## Benchmarks

Create one under **Benchmarks → New benchmark**. A benchmark is a matrix of steps: models × context lengths × thinking levels × load levels. The test type picks which of these vary:

| Type | What varies | Answers |
|---|---|---|
| Load sweep | Concurrent users, or requests per second | Where does throughput plateau, and what happens to latency? |
| Context sweep | Prompt length (exact token counts) | How much slower is a long prompt? |
| Thinking comparison | Thinking off / low / medium / high | What does reasoning cost in latency and tokens? |
| Matrix | Any combination | All of the above at once |
| Single | Nothing | N requests at one load level |

Load can be a **closed loop** (N simulated users, each sending its next request when the previous one finishes) or an **open loop** (requests arrive at a fixed rate, independent of how fast the server answers, which reveals queueing).

Other options: warm-up requests, requests or duration per step, exact output length (`ignore_eos` on vLLM/SGLang), prefix-cache busting, a stop-on-error-rate limit, per-request timeout, extra request body, and SLO targets.

Save a configuration as a **definition** to re-run it with one click. Each change is saved as a new version, and runs remember which version they came from. Definitions can be exported and imported as YAML or JSON.

## Metrics

Measured on the client, on streaming responses, from the moment the request is sent:

| Metric | Definition |
|---|---|
| TTFT | Time to the first streamed token of any kind (reasoning or answer) |
| Time to first answer token | Time to the first non-reasoning token |
| E2E latency | Time to the final chunk |
| TPOT | (E2E − TTFT) / (output tokens − 1) |
| Output speed | Output tokens / (E2E − TTFT), per request |
| Prefill speed | Input tokens / TTFT |
| ITL | Gaps between streamed chunks (per chunk: servers may send several tokens per chunk) |
| Goodput | Requests per second that succeed and meet every SLO target |

Aggregates report min, mean, p50, p90, p95, p99 and max. Token counts come from the server's usage report; if a server doesn't send one, they are estimated with a tokenizer and marked as estimated.

## Export and import

| Format | Where | Use |
|---|---|---|
| JSON | Run page, run list (several runs), comparisons | Everything, including every request; import it into another LLMBench |
| CSV | Run page | One row per request, or one row per step with all percentiles |
| HTML report | Run page, comparisons | Self-contained file with charts, findings and tables |
| Markdown | Run page, comparisons | For issues, wikis and pull requests |

Import runs or definition files with **Benchmarks → Import…**. Runs that already exist are skipped.

## Building

Requirements: Go 1.27+, Node.js (the Docker build uses 24), and `make` (on Windows, run `make` from Git Bash).

| Command | Does |
|---|---|
| `make build` | Builds the frontend and a single binary at `bin/llmbench` |
| `make dev` | Vite dev server on :5173 with live reload, plus the Go backend on :8080 (via [air](https://github.com/air-verse/air)) |
| `make test` | Go tests and frontend type checks |
| `make lint` | `golangci-lint run` |
| `make docker` | Builds the Docker image |

Without `make`:

```bash
cd web && npm install && npm run build && cd ..
cp -r web/build/* internal/ui/dist/
go build -o bin/llmbench ./cmd/llmbench
```

`go build` also works before the frontend is built; the server then shows a placeholder page.

### Antivirus false positives

Some endpoint security products quarantine freshly built, unsigned Go binaries (for example `tmp/llmbench.exe` during `make dev`). If the backend fails to start with "cannot find the file specified", check your antivirus and exclude the project's `tmp/` and `bin/` folders.

## Project layout

```
cmd/llmbench/   CLI entrypoint (serve, version)
internal/       Go backend: API, providers, benchmark runner, metrics, storage, exports
migrations/     SQL migrations, applied automatically at startup
web/            SvelteKit frontend, embedded into the binary at build time
docs/PLAN.md    Design and roadmap
```

Contributor conventions (architecture rules, metric definitions, testing) are in [CLAUDE.md](CLAUDE.md); the full design and roadmap are in [docs/PLAN.md](docs/PLAN.md).
