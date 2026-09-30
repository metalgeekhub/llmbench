// Mirrors the Go JSON types served under /api/v1.

export interface Source {
	id: string;
	name: string;
	type: string;
	base_url: string;
	has_api_key: boolean;
	header_names: string[];
	extra_body: Record<string, unknown> | null;
	timeout_seconds: number;
	tls_skip_verify: boolean;
	models: string[];
	models_auto: boolean;
	managed_by: 'env' | 'ui';
	read_only: boolean;
}

/** Create/update payload. `api_key`/`headers` null or omitted keeps the stored value. */
export interface SourceInput {
	name: string;
	type: string;
	base_url: string;
	api_key?: string | null;
	headers?: Record<string, string> | null;
	extra_body: Record<string, unknown> | null;
	timeout_seconds: number;
	tls_skip_verify: boolean;
	models: string[];
	models_auto: boolean;
}

export interface ModelList {
	models: string[];
	auto: boolean;
	error?: string;
}

export interface Summary {
	count: number;
	min: number;
	mean: number;
	p50: number;
	p90: number;
	p95: number;
	p99: number;
	max: number;
}

export interface Metrics {
	ttft_ms: number | null;
	ttfat_ms: number | null;
	e2e_ms: number;
	tpot_ms: number | null;
	output_tps: number | null;
	prefill_tps: number | null;
	input_tokens: number;
	output_tokens: number;
	reasoning_tokens: number;
	cached_tokens: number;
	tokens_estimated: boolean;
	chunk_count: number;
	itl: Summary;
}

export type RequestStatus = 'ok' | 'error' | 'canceled';

export interface RequestRecord {
	id: string;
	kind: string;
	source_id: string;
	source_name: string;
	model: string;
	session_id: string;
	run_id: string;
	cell_id: string;
	warmup: boolean;
	status: RequestStatus;
	http_status: number;
	error_type: string;
	error_message: string;
	started_at: string;
	metrics: Metrics;
	params: Record<string, unknown> | null;
}

export interface Timeline {
	/** Chunk receive offsets in ms from request start. */
	t: number[];
	/** One char per chunk: 'c' content, 'r' reasoning. */
	k: string;
}

/** '' = server default. */
export type ThinkingLevel = '' | 'off' | 'low' | 'medium' | 'high';
export type ThinkingStyle = '' | 'reasoning_effort' | 'chat_template_kwargs';

export interface ChatParams {
	temperature: number | null;
	max_tokens: number | null;
	system_prompt: string;
	extra_body: Record<string, unknown> | null;
	thinking: ThinkingLevel;
	thinking_style: ThinkingStyle;
}

export interface ChatSession {
	id: string;
	title: string;
	source_id: string;
	model: string;
	params: ChatParams;
	profile_id: string;
	compare_id: string;
	created_at: string;
	updated_at: string;
}

export interface Profile {
	id: string;
	name: string;
	source_id: string;
	model: string;
	params: ChatParams;
	created_at: string;
	updated_at: string;
}

export interface CompareGroup {
	id: string;
	title: string;
	sessions: ChatSession[];
	updated_at: string;
}

export interface ChatMessage {
	id: string;
	session_id: string;
	role: 'user' | 'assistant';
	content: string;
	reasoning: string;
	source_id: string;
	model: string;
	request_id: string;
	/** > 0: a synthetic prompt of this many tokens. */
	synthetic_tokens: number;
	created_at: string;
	request: RequestRecord | null;
}

export interface SessionDetail extends ChatSession {
	messages: ChatMessage[];
}

// --- benchmarks ---

export type TestType = 'single' | 'concurrency_sweep' | 'context_sweep' | 'thinking_comparison' | 'matrix';
export type LoadModel = 'closed' | 'open';

export interface BenchmarkTarget {
	source_id: string;
	model: string;
	/** When set, source and model come from the profile. */
	profile_id?: string;
}

export interface BenchmarkPrompt {
	mode: 'fixed' | 'synthetic';
	text: string;
	input_tokens: number;
	system_prompt: string;
}

export interface BenchmarkConfig {
	name: string;
	type: TestType;
	targets: BenchmarkTarget[];
	prompt: BenchmarkPrompt;
	max_tokens: number | null;
	temperature: number | null;
	ignore_eos: boolean;
	extra_body: Record<string, unknown> | null;
	concurrency: number[];
	load_model: LoadModel;
	arrival_rates: number[];
	max_in_flight: number;
	context_lengths: number[];
	thinking_levels: Exclude<ThinkingLevel, ''>[];
	thinking_style: ThinkingStyle;
	requests_per_cell: number;
	duration_seconds: number;
	warmup_requests: number;
	cache_bust: boolean;
	timeout_seconds: number;
	max_error_rate: number;
	/** Goodput targets; requests meeting all of them count as good. */
	slo?: SLO | null;
}

/** SLO targets; unset fields are not checked. */
export interface SLO {
	ttft_ms?: number;
	tpot_ms?: number;
	e2e_ms?: number;
	min_output_tps?: number;
}

export interface Goodput {
	/** Completed requests (ok + failed); canceled excluded. */
	requests: number;
	good: number;
	ratio: number;
	/** Good requests per second of the step's wall time. */
	per_second: number;
	/** Failed target counts: ttft, tpot, e2e, output_tps, error. */
	violations: Record<string, number>;
}

export interface Aggregate {
	requests: number;
	succeeded: number;
	failed: number;
	canceled: number;
	error_rate: number;
	errors_by_type: Record<string, number>;
	wall_seconds: number;
	request_throughput: number;
	output_throughput: number;
	input_tokens: number;
	output_tokens: number;
	reasoning_tokens: number;
	tokens_estimated: boolean;
	ttft_ms: Summary;
	ttfat_ms: Summary;
	e2e_ms: Summary;
	tpot_ms: Summary;
	output_tps: Summary;
	prefill_tps: Summary;
	itl_ms: Summary;
	/** Client-side only: the step's goodput under the run's SLO (from /goodput). */
	goodput?: Goodput;
}

export type RunStatus =
	| 'pending'
	| 'running'
	| 'completed'
	| 'stopped'
	| 'aborted'
	| 'failed'
	| 'skipped'
	| 'interrupted';

export interface RunCell {
	id: string;
	run_id: string;
	index: number;
	source_id: string;
	source_name: string;
	model: string;
	profile_id: string;
	profile_name: string;
	context_tokens: number;
	thinking: ThinkingLevel;
	concurrency: number;
	arrival_rate: number | null;
	schedule_lag_p95_ms: number | null;
	sample_output: string;
	sample_reasoning: string;
	status: RunStatus;
	summary: Aggregate | null;
	client_cpu_pct: number | null;
	error: string;
	started_at: string | null;
	finished_at: string | null;
}

export interface Progress {
	cell_index: number;
	cell_count: number;
	cell_id: string;
	phase: 'warmup' | 'measuring';
	concurrency: number;
	arrival_rate: number;
	target_requests: number;
	duration_seconds: number;
	warmup_done: number;
	warmup_total: number;
	completed: number;
	failed: number;
	in_flight: number;
	active_users: number;
	output_tokens: number;
	cell_elapsed_seconds: number;
	run_elapsed_seconds: number;
	requests_per_second: number;
	output_tokens_per_second: number;
	recent_errors: string[];
}

export interface Run {
	id: string;
	name: string;
	type: TestType;
	status: RunStatus;
	config: BenchmarkConfig;
	error: string;
	created_at: string;
	started_at: string | null;
	finished_at: string | null;
	cells: RunCell[];
	progress: Progress | null;
	/** Per-second live metrics: in memory while running, persisted after. */
	timeline?: LivePoint[] | null;
	/** The saved definition the run was started from ('' for ad-hoc runs). */
	definition_id: string;
	definition_version: number;
	/** Set on runs imported from an export file. */
	imported_at: string | null;
}

export interface CompareDetail {
	id: string;
	title: string;
	sessions: SessionDetail[];
}

export interface LivePoint {
	t: number;
	cell: number;
	users: number;
	in_flight: number;
	rps: number;
	tps: number;
	errors: number;
	ttft_p50: number | null;
	ttft_p95: number | null;
}

export interface LiveUpdate {
	run: Run;
	points: LivePoint[];
	next: number;
	done: boolean;
}

export interface Comparison {
	id: string;
	name: string;
	run_ids: string[];
	settings: Record<string, unknown> | null;
	created_at: string;
	updated_at: string;
}

// --- saved test definitions ---

export interface Definition {
	id: string;
	name: string;
	description: string;
	/** Every save creates a new version. */
	version: number;
	config: BenchmarkConfig;
	created_at: string;
	updated_at: string;
}

export interface DefinitionVersion {
	version: number;
	config: BenchmarkConfig;
	created_at: string;
}

export interface ImportResult {
	run_id: string;
	name: string;
	status: 'imported' | 'skipped';
	reason?: string;
}
