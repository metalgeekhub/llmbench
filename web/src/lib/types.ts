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

export interface ChatParams {
	temperature: number | null;
	max_tokens: number | null;
	system_prompt: string;
	extra_body: Record<string, unknown> | null;
}

export interface ChatSession {
	id: string;
	title: string;
	source_id: string;
	model: string;
	params: ChatParams;
	created_at: string;
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
	created_at: string;
	request: RequestRecord | null;
}

export interface SessionDetail extends ChatSession {
	messages: ChatMessage[];
}
