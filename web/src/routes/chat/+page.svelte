<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { onMount, tick, untrack } from 'svelte';
	import {
		createSession,
		deleteSession,
		getSession,
		listSessions,
		listSources,
		sendMessage,
		sourceModels
	} from '$lib/api';
	import { ms, relativeTime } from '$lib/format';
	import type { ChatMessage, ChatParams, ChatSession, Source } from '$lib/types';
	import Markdown from '$lib/components/Markdown.svelte';
	import MetricsStrip from '$lib/components/MetricsStrip.svelte';
	import RequestDetail from '$lib/components/RequestDetail.svelte';

	type UIMessage = ChatMessage & { pending?: boolean };

	const PENDING_USER = 'pending-user';
	const PENDING_ASSISTANT = 'pending-assistant';

	let sources = $state<Source[]>([]);
	let sessions = $state<ChatSession[]>([]);
	let sessionId = $state<string | null>(null);
	let messages = $state<UIMessage[]>([]);

	let sourceId = $state('');
	let model = $state('');
	let models = $state<string[]>([]);
	let modelsError = $state('');
	let modelsLoading = $state(false);

	let showParams = $state(false);
	let temperature = $state('');
	let maxTokens = $state('');
	let systemPrompt = $state('');
	let extraBody = $state('');

	let input = $state('');
	let streaming = $state(false);
	let abort: AbortController | null = null;
	let error = $state('');
	let detailId = $state<string | null>(null);
	let scroller = $state<HTMLDivElement>();

	const urlSession = $derived(page.url.searchParams.get('s'));
	const modelOptions = $derived(model && !models.includes(model) ? [model, ...models] : models);
	const extraBodyError = $derived.by(() => {
		if (!extraBody.trim()) return '';
		try {
			const v = JSON.parse(extraBody);
			return v && typeof v === 'object' && !Array.isArray(v) ? '' : 'Must be a JSON object';
		} catch {
			return 'Invalid JSON';
		}
	});

	onMount(() => {
		listSources()
			.then((s) => {
				sources = s;
				if (!sourceId && s.length) sourceId = s[0].id;
			})
			.catch((e) => (error = e.message));
		refreshSessions();
	});

	// Follow the ?s= URL parameter. Only the URL is tracked: send() sets
	// sessionId before navigating, which must not reset the conversation.
	$effect(() => {
		const id = urlSession;
		untrack(() => {
			if (id === sessionId) return;
			if (id) loadSession(id);
			else {
				sessionId = null;
				messages = [];
			}
		});
	});

	// Load the model list whenever the source changes.
	$effect(() => {
		const id = sourceId;
		if (id) untrack(() => loadModels(id, false));
	});

	async function loadModels(id: string, refresh: boolean) {
		modelsLoading = true;
		modelsError = '';
		try {
			const ml = await sourceModels(id, refresh);
			if (id !== sourceId) return;
			models = ml.models;
			modelsError = ml.error ?? '';
			if (!model && ml.models.length) model = ml.models[0];
		} catch (e) {
			modelsError = (e as Error).message;
			models = [];
		} finally {
			modelsLoading = false;
		}
	}

	async function refreshSessions() {
		try {
			sessions = await listSessions();
		} catch (e) {
			error = (e as Error).message;
		}
	}

	async function loadSession(id: string) {
		sessionId = id;
		error = '';
		try {
			const d = await getSession(id);
			if (sessionId !== id) return;
			messages = d.messages;
			if (d.source_id) sourceId = d.source_id;
			if (d.model) model = d.model;
			applyParams(d.params);
			await scrollToBottom(true);
		} catch (e) {
			error = (e as Error).message;
		}
	}

	function applyParams(p: ChatParams) {
		temperature = p.temperature == null ? '' : String(p.temperature);
		maxTokens = p.max_tokens == null ? '' : String(p.max_tokens);
		systemPrompt = p.system_prompt ?? '';
		extraBody = p.extra_body && Object.keys(p.extra_body).length ? JSON.stringify(p.extra_body, null, 2) : '';
	}

	function buildParams(): ChatParams {
		const t = temperature.trim();
		const mt = maxTokens.trim();
		return {
			temperature: t === '' ? null : Number(t),
			max_tokens: mt === '' ? null : Math.trunc(Number(mt)),
			system_prompt: systemPrompt,
			extra_body: extraBody.trim() ? JSON.parse(extraBody) : null
		};
	}

	function onSourceChange() {
		model = '';
		models = [];
	}

	function newChat() {
		error = '';
		goto('/chat');
	}

	async function removeSession(s: ChatSession) {
		if (!confirm(`Delete chat "${s.title || 'Untitled'}"? Request history is kept.`)) return;
		try {
			await deleteSession(s.id);
			if (s.id === sessionId) goto('/chat');
			await refreshSessions();
		} catch (e) {
			error = (e as Error).message;
		}
	}

	function blankMessage(id: string, role: 'user' | 'assistant', content = ''): UIMessage {
		return {
			id,
			session_id: sessionId ?? '',
			role,
			content,
			reasoning: '',
			source_id: sourceId,
			model,
			request_id: '',
			created_at: new Date().toISOString(),
			request: null,
			pending: true
		};
	}

	function replaceMessage(id: string, m: ChatMessage) {
		const i = messages.findIndex((x) => x.id === id);
		if (i >= 0) messages[i] = m;
		else messages.push(m);
	}

	async function send() {
		const content = input.trim();
		if (!content || streaming) return;
		if (!sourceId || !model) {
			error = 'Select a source and a model first.';
			return;
		}
		if (extraBodyError) {
			error = `Extra body: ${extraBodyError}`;
			showParams = true;
			return;
		}
		const params = buildParams();
		if ((params.temperature != null && isNaN(params.temperature)) || (params.max_tokens != null && isNaN(params.max_tokens))) {
			error = 'Temperature and max tokens must be numbers.';
			showParams = true;
			return;
		}

		error = '';
		streaming = true;
		input = '';
		abort = new AbortController();
		const signal = abort.signal;
		let id = sessionId;
		try {
			if (!id) {
				const s = await createSession({ source_id: sourceId, model, params });
				id = s.id;
				sessionId = id;
				goto(`/chat?s=${id}`, { keepFocus: true, noScroll: true });
			}
			messages.push(blankMessage(PENDING_USER, 'user', content), blankMessage(PENDING_ASSISTANT, 'assistant'));
			await scrollToBottom(true);

			await sendMessage(
				id,
				{ content, source_id: sourceId, model, params },
				{
					onUserMessage: (m) => replaceMessage(PENDING_USER, m),
					onDelta: (kind, text) => {
						const a = messages.find((x) => x.id === PENDING_ASSISTANT);
						if (!a) return;
						if (kind === 'content') a.content += text;
						else a.reasoning += text;
						scrollToBottom(false);
					},
					onDone: (m) => replaceMessage(PENDING_ASSISTANT, m),
					onError: (msg) => (error = msg)
				},
				signal
			);
		} catch (e) {
			if (signal.aborted && id) {
				// The server records the canceled request; reload to show it.
				setTimeout(() => id && sessionId === id && loadSession(id), 400);
			} else {
				error = (e as Error).message;
				messages = messages.filter((m) => !m.pending);
				input = content;
			}
		} finally {
			streaming = false;
			abort = null;
			refreshSessions();
		}
	}

	function stop() {
		abort?.abort();
	}

	function onKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
			e.preventDefault();
			send();
		}
	}

	async function scrollToBottom(force: boolean) {
		await tick();
		if (!scroller) return;
		const nearBottom = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 120;
		if (force || nearBottom) scroller.scrollTop = scroller.scrollHeight;
	}

	function thoughtFor(m: UIMessage): string {
		const r = m.request?.metrics;
		if (!r || r.ttft_ms == null) return '';
		const end = r.ttfat_ms ?? r.e2e_ms;
		return `thought for ${ms(end - r.ttft_ms)}`;
	}
</script>

<div class="flex h-full">
	<!-- Sessions -->
	<aside class="flex w-64 shrink-0 flex-col border-r border-stone-200 bg-white">
		<div class="p-3">
			<button
				class="w-full rounded-md border border-stone-300 px-3 py-1.5 text-sm font-medium hover:bg-stone-50"
				onclick={newChat}
			>
				+ New chat
			</button>
		</div>
		<ul class="flex-1 overflow-y-auto px-2 pb-3 text-sm">
			{#each sessions as s (s.id)}
				<li class="group relative">
					<a
						href="/chat?s={s.id}"
						class="block rounded-md px-3 py-2 pr-8 {s.id === sessionId
							? 'bg-stone-100 text-stone-900'
							: 'text-stone-700 hover:bg-stone-50'}"
					>
						<div class="truncate">{s.title || 'Untitled'}</div>
						<div class="truncate text-xs text-stone-400">{s.model} · {relativeTime(s.updated_at)}</div>
					</a>
					<button
						class="absolute top-2 right-1 hidden rounded px-1.5 text-stone-400 group-hover:block hover:bg-stone-200 hover:text-stone-700"
						title="Delete chat"
						aria-label="Delete chat"
						onclick={() => removeSession(s)}>✕</button
					>
				</li>
			{:else}
				<li class="px-3 py-2 text-stone-400">No chats yet.</li>
			{/each}
		</ul>
	</aside>

	<!-- Conversation -->
	<section class="flex min-w-0 flex-1 flex-col">
		<div class="flex flex-wrap items-center gap-2 border-b border-stone-200 bg-white px-4 py-2 text-sm">
			<label class="flex items-center gap-1.5">
				<span class="text-stone-500">Source</span>
				<select
					class="rounded-md border border-stone-300 bg-white px-2 py-1"
					bind:value={sourceId}
					onchange={onSourceChange}
					disabled={streaming}
				>
					{#each sources as s (s.id)}
						<option value={s.id}>{s.name}{s.read_only ? ' (env)' : ''}</option>
					{/each}
				</select>
			</label>
			<label class="flex items-center gap-1.5">
				<span class="text-stone-500">Model</span>
				<select class="max-w-72 rounded-md border border-stone-300 bg-white px-2 py-1" bind:value={model} disabled={streaming}>
					{#each modelOptions as m (m)}
						<option value={m}>{m}</option>
					{/each}
				</select>
			</label>
			<button
				class="rounded px-2 py-1 text-stone-500 hover:bg-stone-100 disabled:opacity-40"
				title="Refresh model list"
				aria-label="Refresh model list"
				disabled={!sourceId || modelsLoading}
				onclick={() => loadModels(sourceId, true)}>↻</button
			>
			{#if modelsError}
				<span class="truncate text-xs text-red-700" title={modelsError}>⚠ Could not list models: {modelsError}</span>
			{/if}
			<div class="flex-1"></div>
			<button
				class="rounded-md px-2.5 py-1 {showParams ? 'bg-stone-100 text-stone-900' : 'text-stone-600 hover:bg-stone-100'}"
				onclick={() => (showParams = !showParams)}
			>
				Parameters
			</button>
		</div>

		{#if sources.length === 0}
			<div class="m-6 rounded-md border border-dashed border-stone-300 p-6 text-sm text-stone-600">
				No sources configured. <a href="/sources" class="text-blue-700 underline">Add a source</a> or define one with
				<code class="rounded bg-stone-100 px-1">LLMB_SOURCE_&lt;NAME&gt;_*</code> environment variables.
			</div>
		{/if}

		<div class="flex min-h-0 flex-1">
			<div bind:this={scroller} class="flex-1 overflow-y-auto px-4 py-6">
				<div class="mx-auto flex max-w-3xl flex-col gap-5">
					{#each messages as m (m.id)}
						{#if m.role === 'user'}
							<div class="self-end rounded-lg bg-blue-50 px-4 py-2.5 whitespace-pre-wrap text-stone-900 max-w-[85%]">
								{m.content}
							</div>
						{:else}
							<div class="max-w-full">
								<div class="mb-1 text-xs text-stone-400">{m.model}</div>
								{#if m.reasoning}
									<details class="mb-2 rounded-md border border-stone-200 bg-white" open={m.pending}>
										<summary class="cursor-pointer px-3 py-1.5 text-xs text-stone-500">
											Reasoning {#if m.pending && !m.content}<span class="animate-pulse">(thinking…)</span>{:else if thoughtFor(m)}({thoughtFor(m)}){/if}
										</summary>
										<div class="border-t border-stone-100 px-3 py-2">
											<Markdown source={m.reasoning} class="prose-sm text-stone-600" />
										</div>
									</details>
								{/if}
								{#if m.content}
									<Markdown source={m.content} />
								{:else if m.pending && !m.reasoning}
									<div class="animate-pulse text-stone-400">●●●</div>
								{/if}
								{#if m.request}
									{#if m.request.error_message && m.request.status !== 'canceled'}
										<div class="mt-1 rounded bg-red-50 px-3 py-2 text-sm text-red-800">
											⚠ {m.request.error_message}
										</div>
									{/if}
									<MetricsStrip request={m.request} onclick={() => (detailId = m.request_id)} />
								{/if}
							</div>
						{/if}
					{:else}
						{#if sources.length > 0}
							<p class="mt-20 text-center text-sm text-stone-400">
								Send a message. Every response is measured: TTFT, latency, tokens per second.
							</p>
						{/if}
					{/each}
				</div>
			</div>

			{#if showParams}
				<aside class="w-72 shrink-0 overflow-y-auto border-l border-stone-200 bg-white p-4 text-sm">
					<h2 class="mb-3 font-semibold">Parameters</h2>
					<label class="mb-3 block">
						<span class="mb-1 block text-xs text-stone-500">Temperature</span>
						<input class="w-full rounded-md border border-stone-300 px-2 py-1" type="number" step="0.1" min="0" max="2" placeholder="server default" bind:value={temperature} />
					</label>
					<label class="mb-3 block">
						<span class="mb-1 block text-xs text-stone-500">Max tokens</span>
						<input class="w-full rounded-md border border-stone-300 px-2 py-1" type="number" min="1" step="1" placeholder="server default" bind:value={maxTokens} />
					</label>
					<label class="mb-3 block">
						<span class="mb-1 block text-xs text-stone-500">System prompt</span>
						<textarea class="w-full rounded-md border border-stone-300 px-2 py-1" rows="4" bind:value={systemPrompt}></textarea>
					</label>
					<label class="mb-1 block">
						<span class="mb-1 block text-xs text-stone-500">Extra body (JSON, merged into the request)</span>
						<textarea
							class="w-full rounded-md border px-2 py-1 font-mono text-xs {extraBodyError ? 'border-red-400' : 'border-stone-300'}"
							rows="5"
							placeholder={'{"top_k": 20}'}
							bind:value={extraBody}
						></textarea>
					</label>
					{#if extraBodyError}<p class="text-xs text-red-700">{extraBodyError}</p>{/if}
					<p class="mt-3 text-xs text-stone-400">Parameters are saved with the chat when you send a message.</p>
				</aside>
			{/if}
		</div>

		{#if error}
			<div class="mx-4 mb-2 flex items-start justify-between gap-3 rounded-md bg-red-50 px-3 py-2 text-sm text-red-800">
				<span>⚠ {error}</span>
				<button class="text-red-600 hover:text-red-900" aria-label="Dismiss" onclick={() => (error = '')}>✕</button>
			</div>
		{/if}

		<div class="border-t border-stone-200 bg-white p-3">
			<div class="mx-auto flex max-w-3xl items-end gap-2">
				<textarea
					class="max-h-48 min-h-10 flex-1 resize-y rounded-md border border-stone-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none"
					rows="2"
					placeholder="Message (Enter to send, Shift+Enter for a new line)"
					bind:value={input}
					onkeydown={onKeydown}
					disabled={sources.length === 0}
				></textarea>
				{#if streaming}
					<button class="rounded-md bg-stone-800 px-4 py-2 text-sm font-medium text-white hover:bg-stone-700" onclick={stop}>
						Stop
					</button>
				{:else}
					<button
						class="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40"
						disabled={!input.trim() || !model}
						onclick={send}
					>
						Send
					</button>
				{/if}
			</div>
		</div>
	</section>
</div>

{#if detailId}
	<RequestDetail requestId={detailId} onclose={() => (detailId = null)} />
{/if}
