// Typed client for the LLMBench API. All backend calls go through here.

import type {
	BenchmarkConfig,
	ChatMessage,
	ChatParams,
	ChatSession,
	CompareDetail,
	Comparison,
	CompareGroup,
	Definition,
	DefinitionVersion,
	Goodput,
	ImportResult,
	Profile,
	ModelList,
	RequestRecord,
	Run,
	LiveUpdate,
	SLO,
	SessionDetail,
	Source,
	SourceInput,
	Timeline
} from './types';

const BASE = '/api/v1';

export class ApiError extends Error {
	constructor(
		message: string,
		public status: number
	) {
		super(message);
	}
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
	const init: RequestInit = {
		method,
		headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
		body: body === undefined ? undefined : JSON.stringify(body)
	};
	let res: Response;
	try {
		res = await fetch(BASE + path, init);
	} catch (e) {
		// Network-level failure (the browser may also drop a queued request):
		// GETs are safe to retry once.
		if (method !== 'GET' || !(e instanceof TypeError)) throw e;
		await new Promise((r) => setTimeout(r, 200));
		res = await fetch(BASE + path, init);
	}
	if (!res.ok) {
		throw new ApiError(await errorMessage(res), res.status);
	}
	if (res.status === 204) return undefined as T;
	return res.json() as Promise<T>;
}

async function errorMessage(res: Response): Promise<string> {
	try {
		const data = await res.json();
		if (data && typeof data.error === 'string') return data.error;
	} catch {
		// not JSON
	}
	return `${res.status} ${res.statusText}`;
}

// --- sources ---

export const listSources = () =>
	request<{ sources: Source[] }>('GET', '/sources').then((r) => r.sources);
export const createSource = (input: SourceInput) => request<Source>('POST', '/sources', input);
export const updateSource = (id: string, input: SourceInput) =>
	request<Source>('PUT', `/sources/${encodeURIComponent(id)}`, input);
export const deleteSource = (id: string) =>
	request<void>('DELETE', `/sources/${encodeURIComponent(id)}`);
export const sourceModels = (id: string, refresh = false) =>
	request<ModelList>(
		'GET',
		`/sources/${encodeURIComponent(id)}/models${refresh ? '?refresh=true' : ''}`
	);
export const discoverModels = (id: string) =>
	request<{ models: string[] }>('POST', `/sources/${encodeURIComponent(id)}/models/discover`).then(
		(r) => r.models
	);

// --- chat ---

export interface SessionInput {
	title?: string;
	source_id?: string;
	model?: string;
	params?: ChatParams;
	profile_id?: string;
	compare_id?: string;
}

export const listSessions = (compareId = '') =>
	request<{ sessions: ChatSession[] }>(
		'GET',
		`/chat/sessions${compareId ? `?compare_id=${encodeURIComponent(compareId)}` : ''}`
	).then((r) => r.sessions);
export const createSession = (input: SessionInput) =>
	request<ChatSession>('POST', '/chat/sessions', input);
export const getSession = (id: string) =>
	request<SessionDetail>('GET', `/chat/sessions/${encodeURIComponent(id)}`);
export const updateSession = (id: string, input: SessionInput) =>
	request<ChatSession>('PATCH', `/chat/sessions/${encodeURIComponent(id)}`, input);
export const deleteSession = (id: string) =>
	request<void>('DELETE', `/chat/sessions/${encodeURIComponent(id)}`);

export interface SendInput {
	content: string;
	source_id?: string;
	model?: string;
	params?: ChatParams;
	profile_id?: string;
	/** Send a synthetic prompt of this many tokens; content becomes its instruction. */
	synthetic_tokens?: number;
}

export interface StreamHandlers {
	onUserMessage?: (m: ChatMessage) => void;
	onDelta?: (kind: 'content' | 'reasoning', text: string) => void;
	onDone?: (m: ChatMessage) => void;
	onError?: (message: string) => void;
}

/**
 * Sends a chat message and consumes the SSE response. The endpoint is a POST,
 * so this reads the stream with fetch instead of EventSource.
 */
export async function sendMessage(
	sessionId: string,
	input: SendInput,
	handlers: StreamHandlers,
	signal?: AbortSignal
): Promise<void> {
	const res = await fetch(`${BASE}/chat/sessions/${encodeURIComponent(sessionId)}/messages`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
		body: JSON.stringify(input),
		signal
	});
	if (!res.ok || !res.body) {
		throw new ApiError(await errorMessage(res), res.status);
	}

	const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
	let buffer = '';
	for (;;) {
		const { value, done } = await reader.read();
		if (done) break;
		buffer += value;
		let sep: number;
		while ((sep = buffer.indexOf('\n\n')) >= 0) {
			const block = buffer.slice(0, sep);
			buffer = buffer.slice(sep + 2);
			dispatch(block, handlers);
		}
	}
	if (buffer.trim()) dispatch(buffer, handlers);
}

function dispatch(block: string, h: StreamHandlers) {
	let event = 'message';
	const data: string[] = [];
	for (const line of block.split('\n')) {
		if (line.startsWith('event:')) event = line.slice(6).trim();
		else if (line.startsWith('data:')) data.push(line.slice(5).trimStart());
	}
	if (data.length === 0) return;
	const payload = JSON.parse(data.join('\n'));
	switch (event) {
		case 'user_message':
			h.onUserMessage?.(payload);
			break;
		case 'delta':
			h.onDelta?.(payload.kind, payload.text);
			break;
		case 'done':
			h.onDone?.(payload.message);
			break;
		case 'error':
			h.onError?.(payload.error);
			break;
	}
}

// --- requests ---

export interface RequestFilter {
	source_id?: string;
	model?: string;
	status?: string;
	kind?: string;
	run_id?: string;
	cell_id?: string;
	limit?: number;
	offset?: number;
}

export function listRequests(f: RequestFilter = {}) {
	const q = new URLSearchParams();
	for (const [k, v] of Object.entries(f)) {
		if (v !== undefined && v !== '') q.set(k, String(v));
	}
	return request<{ requests: RequestRecord[]; total: number }>('GET', `/requests?${q}`);
}
export const listRequestModels = () =>
	request<{ models: string[] }>('GET', '/requests/models').then((r) => r.models);
export const getRequest = (id: string) =>
	request<{ request: RequestRecord; timeline: Timeline | null }>(
		'GET',
		`/requests/${encodeURIComponent(id)}`
	);

// --- benchmarks ---

export const listRuns = () => request<{ runs: Run[] }>('GET', '/runs').then((r) => r.runs);
export const startRun = (config: BenchmarkConfig) => request<Run>('POST', '/runs', config);
export const getRun = (id: string) => request<Run>('GET', `/runs/${encodeURIComponent(id)}`);
export const stopRun = (id: string) => request<void>('POST', `/runs/${encodeURIComponent(id)}/stop`);
export const deleteRun = (id: string) => request<void>('DELETE', `/runs/${encodeURIComponent(id)}`);
/** Server-Sent Events URL for a run's live dashboard (see LiveUpdate). */
export const runEventsUrl = (id: string) => `${BASE}/runs/${encodeURIComponent(id)}/events`;

// --- model profiles ---

export interface ProfileInput {
	name: string;
	source_id: string;
	model: string;
	params: ChatParams;
}

export const listProfiles = () =>
	request<{ profiles: Profile[] }>('GET', '/profiles').then((r) => r.profiles);
export const createProfile = (input: ProfileInput) => request<Profile>('POST', '/profiles', input);
export const updateProfile = (id: string, input: ProfileInput) =>
	request<Profile>('PUT', `/profiles/${encodeURIComponent(id)}`, input);
export const deleteProfile = (id: string) =>
	request<void>('DELETE', `/profiles/${encodeURIComponent(id)}`);

// --- side-by-side compare ---

export const listCompares = () =>
	request<{ compares: CompareGroup[] }>('GET', '/chat/compares').then((r) => r.compares);
export const getCompare = (id: string) =>
	request<CompareDetail>('GET', `/chat/compares/${encodeURIComponent(id)}`);
export const deleteCompare = (id: string) =>
	request<void>('DELETE', `/chat/compares/${encodeURIComponent(id)}`);

// --- saved run comparisons ---

export interface ComparisonInput {
	name: string;
	run_ids: string[];
	settings?: Record<string, unknown>;
}

export const listComparisons = () =>
	request<{ comparisons: Comparison[] }>('GET', '/comparisons').then((r) => r.comparisons);
export const getComparison = (id: string) =>
	request<Comparison>('GET', `/comparisons/${encodeURIComponent(id)}`);
export const createComparison = (input: ComparisonInput) =>
	request<Comparison>('POST', '/comparisons', input);
export const updateComparison = (id: string, input: ComparisonInput) =>
	request<Comparison>('PUT', `/comparisons/${encodeURIComponent(id)}`, input);
export const deleteComparison = (id: string) =>
	request<void>('DELETE', `/comparisons/${encodeURIComponent(id)}`);

export type DistributionMetric = 'ttft_ms' | 'ttfat_ms' | 'e2e_ms' | 'tpot_ms' | 'output_tps';
/** Per-request values of one metric for each cell of a run (for histograms). */
export const getRunDistribution = (id: string, metric: DistributionMetric) =>
	request<{ metric: string; cells: Record<string, number[]> }>(
		'GET',
		`/runs/${encodeURIComponent(id)}/distribution?metric=${metric}`
	);

// --- goodput / SLOs ---

/** Evaluates SLO targets against a run's requests; `slo` overrides the saved targets (what-if). */
export function getRunGoodput(id: string, slo?: SLO) {
	const q = new URLSearchParams();
	for (const [k, v] of Object.entries(slo ?? {})) {
		if (v !== undefined && v !== null) q.set(k, String(v));
	}
	const qs = q.toString();
	return request<{ slo: SLO; cells: Record<string, Goodput> }>(
		'GET',
		`/runs/${encodeURIComponent(id)}/goodput${qs ? `?${qs}` : ''}`
	);
}
/** Saves (or with null, clears) the SLO targets stored with a run. */
export const updateRunSLO = (id: string, slo: SLO | null) =>
	request<Run>('PATCH', `/runs/${encodeURIComponent(id)}`, { slo });

// --- export & import ---

/** Download URL of a JSON bundle with the given runs (re-importable). */
export const exportRunsUrl = (ids: string[]) =>
	`${BASE}/export?runs=${ids.map(encodeURIComponent).join(',')}`;
/** Download URL of a run's requests (one row per request) or steps as CSV. */
export const exportRunCsvUrl = (id: string, table: 'requests' | 'cells') =>
	`${BASE}/runs/${encodeURIComponent(id)}/export.csv?table=${table}`;

async function upload<T>(path: string, file: Blob): Promise<T> {
	const res = await fetch(BASE + path, { method: 'POST', body: file });
	if (!res.ok) throw new ApiError(await errorMessage(res), res.status);
	return res.json() as Promise<T>;
}

/** Imports the runs of an exported JSON bundle; runs already present are skipped. */
export const importRuns = (file: Blob) =>
	upload<{ results: ImportResult[] }>('/import', file).then((r) => r.results);

// --- saved test definitions ---

export interface DefinitionInput {
	name: string;
	description: string;
	config: BenchmarkConfig;
}

export const listDefinitions = () =>
	request<{ definitions: Definition[] }>('GET', '/definitions').then((r) => r.definitions);
export const getDefinition = (id: string) =>
	request<Definition>('GET', `/definitions/${encodeURIComponent(id)}`);
export const createDefinition = (input: DefinitionInput) =>
	request<Definition>('POST', '/definitions', input);
/** Saves a new version of the definition. */
export const updateDefinition = (id: string, input: DefinitionInput) =>
	request<Definition>('PUT', `/definitions/${encodeURIComponent(id)}`, input);
export const deleteDefinition = (id: string) =>
	request<void>('DELETE', `/definitions/${encodeURIComponent(id)}`);
export const definitionVersions = (id: string) =>
	request<{ versions: DefinitionVersion[] }>(
		'GET',
		`/definitions/${encodeURIComponent(id)}/versions`
	).then((r) => r.versions);
/** Starts a run from the latest version. */
export const runDefinition = (id: string) =>
	request<Run>('POST', `/definitions/${encodeURIComponent(id)}/run`);
export const exportDefinitionUrl = (id: string, format: 'yaml' | 'json') =>
	`${BASE}/definitions/${encodeURIComponent(id)}/export?format=${format}`;
/** Imports a definition file (YAML or JSON); a matching name becomes a new version. */
export const importDefinition = (file: Blob) => upload<Definition>('/definitions/import', file);
