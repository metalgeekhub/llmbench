// Typed client for the LLMBench API. All backend calls go through here.

import type {
	ChatMessage,
	ChatParams,
	ChatSession,
	ModelList,
	RequestRecord,
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
	const res = await fetch(BASE + path, {
		method,
		headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
		body: body === undefined ? undefined : JSON.stringify(body)
	});
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
}

export const listSessions = () =>
	request<{ sessions: ChatSession[] }>('GET', '/chat/sessions').then((r) => r.sessions);
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
