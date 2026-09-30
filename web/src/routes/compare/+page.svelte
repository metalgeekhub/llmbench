<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { onMount, tick, untrack } from 'svelte';
	import {
		createSession,
		deleteCompare,
		getCompare,
		listCompares,
		listProfiles,
		listSources,
		sendMessage,
		sourceModels
	} from '$lib/api';
	import { int, ms, rate, relativeTime } from '$lib/format';
	import { emptyParams, normalizeParams, thinkingLabel } from '$lib/params';
	import { seriesColor } from '$lib/palette';
	import type { ChatMessage, ChatParams, CompareGroup, Metrics, Profile, Source } from '$lib/types';
	import Markdown from '$lib/components/Markdown.svelte';
	import MetricsStrip from '$lib/components/MetricsStrip.svelte';
	import ParamsEditor from '$lib/components/ParamsEditor.svelte';
	import RequestDetail from '$lib/components/RequestDetail.svelte';
	import SyntheticControl from '$lib/components/SyntheticControl.svelte';
	import UserPrompt from '$lib/components/UserPrompt.svelte';

	type UIMessage = ChatMessage & { pending?: boolean };

	interface Column {
		key: string;
		profileId: string;
		sourceId: string;
		model: string;
		params: ChatParams;
		paramsError: string;
		showParams: boolean;
		sessionId: string | null;
		messages: UIMessage[];
		error: string;
	}

	const MIN_COLUMNS = 2;
	const MAX_COLUMNS = 4;

	let sources = $state<Source[]>([]);
	let profiles = $state<Profile[]>([]);
	let compares = $state<CompareGroup[]>([]);
	let modelOptions = $state<Record<string, string[]>>({});

	let compareId = $state<string | null>(null);
	let columns = $state<Column[]>([]);
	let input = $state('');
	let synthetic = $state(false);
	let synthTokens = $state(4096);
	let streaming = $state(false);
	let sequential = $state(false);
	let sequentialTouched = false;
	let abort: AbortController | null = null;
	let error = $state('');
	let detailId = $state<string | null>(null);
	let scroller = $state<HTMLDivElement>();

	const urlCompare = $derived(page.url.searchParams.get('c'));
	const locked = $derived(compareId !== null);
	const profileById = $derived(new Map(profiles.map((p) => [p.id, p])));
	const sourceById = $derived(new Map(sources.map((s) => [s.id, s])));

	function uid(): string {
		// crypto.randomUUID needs a secure context; LAN access over http has none.
		if (typeof crypto !== 'undefined' && 'randomUUID' in crypto && globalThis.isSecureContext) return crypto.randomUUID();
		return Array.from({ length: 4 }, () => Math.random().toString(16).slice(2, 10)).join('-');
	}

	function newColumn(from?: Partial<Column>): Column {
		const sourceId = from?.sourceId ?? sources[0]?.id ?? '';
		return {
			key: uid(),
			profileId: '',
			sourceId,
			model: from?.model ?? modelOptions[sourceId]?.[0] ?? '',
			params: emptyParams(),
			paramsError: '',
			showParams: false,
			sessionId: null,
			messages: [],
			error: '',
			...from
		};
	}

	onMount(async () => {
		try {
			[sources, profiles] = await Promise.all([listSources(), listProfiles()]);
		} catch (e) {
			error = (e as Error).message;
		}
		refreshCompares();
		if (!urlCompare && columns.length === 0) resetSetup();
	});

	// Follow ?c=. sendAll() sets compareId before navigating, which must not
	// reload the page state.
	$effect(() => {
		const id = urlCompare;
		untrack(() => {
			if (id === compareId) return;
			if (id) loadCompare(id);
			else {
				compareId = null;
				resetSetup();
			}
		});
	});

	// Default to one-at-a-time when two columns share a source, so they don't
	// compete for the same server (unless the user chose otherwise).
	$effect(() => {
		const shared = new Set(columns.map((c) => c.sourceId)).size < columns.length;
		if (!sequentialTouched) sequential = shared;
	});

	function resetSetup() {
		if (sources.length === 0) {
			columns = [];
			return;
		}
		// Start with two different models where possible.
		const a = newColumn();
		columns = [a, newColumn({ sourceId: a.sourceId })];
		for (const c of columns) loadModels(c.sourceId);
	}

	async function refreshCompares() {
		try {
			compares = await listCompares();
		} catch (e) {
			error = (e as Error).message;
		}
	}

	async function loadModels(sourceId: string) {
		if (!sourceId || modelOptions[sourceId]) return;
		try {
			const ml = await sourceModels(sourceId);
			modelOptions[sourceId] = ml.models;
			// Fill empty columns, spreading different models across them.
			const used = new Set<string>();
			for (const c of columns) {
				if (c.sourceId !== sourceId || c.profileId) continue;
				if (!c.model) c.model = ml.models.find((m) => !used.has(m)) ?? ml.models[0] ?? '';
				used.add(c.model);
			}
		} catch {
			modelOptions[sourceId] = [];
		}
	}

	async function loadCompare(id: string) {
		compareId = id;
		error = '';
		try {
			const d = await getCompare(id);
			if (compareId !== id) return;
			columns = d.sessions.map((s) =>
				newColumn({
					sessionId: s.id,
					profileId: s.profile_id ?? '',
					sourceId: s.source_id,
					model: s.model,
					params: normalizeParams(s.params),
					messages: s.messages
				})
			);
			await scrollToBottom(true);
		} catch (e) {
			error = (e as Error).message;
		}
	}

	function applyProfile(c: Column, id: string) {
		c.profileId = id;
		const p = profileById.get(id);
		if (p) {
			c.sourceId = p.source_id;
			c.model = p.model;
			c.params = normalizeParams($state.snapshot(p.params));
		} else {
			c.params = emptyParams();
		}
		loadModels(c.sourceId);
	}

	function changeSource(c: Column, id: string) {
		c.sourceId = id;
		c.profileId = '';
		c.model = modelOptions[id]?.[0] ?? '';
		loadModels(id);
	}

	function columnTitle(c: Column): string {
		return profileById.get(c.profileId)?.name ?? c.model ?? '?';
	}

	function columnSubtitle(c: Column): string {
		const parts = [];
		if (c.profileId) parts.push(c.model);
		parts.push(sourceById.get(c.sourceId)?.name ?? c.sourceId);
		const t = thinkingLabel(c.params);
		if (t) parts.push(t);
		return parts.join(' · ');
	}

	function newComparison() {
		error = '';
		goto('/compare');
	}

	async function removeCompare(g: CompareGroup) {
		if (!confirm(`Delete comparison "${g.title || 'Untitled'}"? Request history is kept.`)) return;
		try {
			await deleteCompare(g.id);
			if (g.id === compareId) goto('/compare');
			await refreshCompares();
		} catch (e) {
			error = (e as Error).message;
		}
	}

	function pending(c: Column, role: 'user' | 'assistant', content = ''): UIMessage {
		return {
			id: `pending-${role}-${c.key}`,
			session_id: c.sessionId ?? '',
			role,
			content,
			reasoning: '',
			source_id: c.sourceId,
			model: c.model,
			request_id: '',
			created_at: new Date().toISOString(),
			request: null,
			synthetic_tokens: 0,
			pending: true
		};
	}

	function replace(c: Column, id: string, m: ChatMessage) {
		const i = c.messages.findIndex((x) => x.id === id);
		if (i >= 0) c.messages[i] = m;
		else c.messages.push(m);
	}

	async function sendAll() {
		const content = input.trim();
		if ((!content && !synthetic) || streaming) return;
		const syntheticTokens = synthetic ? synthTokens : 0;
		const bad = columns.find((c) => !c.sourceId || !c.model.trim() || c.paramsError);
		if (bad) {
			error = bad.paramsError || 'Every column needs a source and a model.';
			return;
		}
		error = '';
		streaming = true;
		input = '';
		abort = new AbortController();
		const signal = abort.signal;
		try {
			if (!compareId) {
				const id = uid();
				for (const c of columns) {
					const s = await createSession({
						source_id: c.sourceId,
						model: c.model.trim(),
						params: $state.snapshot(c.params),
						profile_id: c.profileId,
						compare_id: id
					});
					c.sessionId = s.id;
				}
				compareId = id;
				goto(`/compare?c=${id}`, { keepFocus: true, noScroll: true });
			}
			for (const c of columns) {
				c.error = '';
				// The server builds the real synthetic text; show a placeholder until it echoes it back.
				const instruction = content || 'Continue the story above in as much detail as you can.';
				const user = syntheticTokens
					? { ...pending(c, 'user', `…\n\n${instruction}`), synthetic_tokens: syntheticTokens }
					: pending(c, 'user', content);
				c.messages.push(user, pending(c, 'assistant'));
			}
			await scrollToBottom(true);

			const runOne = async (c: Column) => {
				const userId = `pending-user-${c.key}`;
				const asstId = `pending-assistant-${c.key}`;
				try {
					await sendMessage(
						c.sessionId!,
						{ content, synthetic_tokens: syntheticTokens },
						{
							onUserMessage: (m) => replace(c, userId, m),
							onDelta: (kind, text) => {
								const a = c.messages.find((x) => x.id === asstId);
								if (!a) return;
								if (kind === 'content') a.content += text;
								else a.reasoning += text;
								scrollToBottom(false);
							},
							onDone: (m) => replace(c, asstId, m),
							onError: (msg) => (c.error = msg)
						},
						signal
					);
				} catch (e) {
					if (!signal.aborted) {
						c.error = (e as Error).message;
						c.messages = c.messages.filter((m) => !m.pending);
					}
				}
			};
			if (sequential) {
				for (const c of columns) {
					if (signal.aborted) break;
					await runOne(c);
				}
			} else {
				await Promise.all(columns.map(runOne));
			}
			if (signal.aborted && compareId) {
				const id = compareId;
				setTimeout(() => compareId === id && loadCompare(id), 400);
			}
		} catch (e) {
			error = (e as Error).message;
		} finally {
			streaming = false;
			abort = null;
			refreshCompares();
		}
	}

	function onKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
			e.preventDefault();
			sendAll();
		}
	}

	async function scrollToBottom(force: boolean) {
		await tick();
		if (!scroller) return;
		const near = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 160;
		if (force || near) scroller.scrollTop = scroller.scrollHeight;
	}

	// Align messages into turns: each user prompt with every column's reply.
	interface Turn {
		prompt: string;
		promptSynthetic: number;
		replies: (UIMessage | null)[];
	}
	const turns = $derived.by(() => {
		const perColumn = columns.map((c) => {
			const t: { user: UIMessage; reply: UIMessage | null }[] = [];
			for (const m of c.messages) {
				if (m.role === 'user') t.push({ user: m, reply: null });
				else if (t.length) t[t.length - 1].reply = m;
			}
			return t;
		});
		const n = Math.max(0, ...perColumn.map((t) => t.length));
		const out: Turn[] = [];
		for (let i = 0; i < n; i++) {
			const user = perColumn.find((t) => t[i])?.[i].user;
			out.push({
				prompt: user?.content ?? '',
				promptSynthetic: user?.synthetic_tokens ?? 0,
				replies: perColumn.map((t) => t[i]?.reply ?? null)
			});
		}
		return out;
	});

	// Summary rows: [label, value getter, formatter, lower is better].
	const summaryRows: [string, (m: Metrics) => number | null, (v: number | null) => string, boolean | null][] = [
		['Time to first token', (m) => m.ttft_ms, ms, true],
		['Time to first answer', (m) => m.ttfat_ms, ms, true],
		['End-to-end latency', (m) => m.e2e_ms, ms, true],
		['Time per output token', (m) => m.tpot_ms, ms, true],
		['Output speed', (m) => m.output_tps, (v) => rate(v), false],
		['Output tokens', (m) => m.output_tokens, int, null]
	];

	function bestIndex(values: (number | null)[], lowerIsBetter: boolean | null): number {
		// null: no "better" direction (e.g. output length), so nothing is highlighted.
		if (lowerIsBetter === null) return -1;
		let best = -1;
		values.forEach((v, i) => {
			if (v == null) return;
			if (best < 0 || (lowerIsBetter ? v < values[best]! : v > values[best]!)) best = i;
		});
		// Only highlight when there is something to compare.
		const present = values.filter((v): v is number => v != null);
		if (present.length < 2) return -1;
		// Within 1% of the runner-up is a tie: no winner.
		const others = present.filter((_, i) => i !== present.indexOf(values[best]!));
		const runnerUp = lowerIsBetter ? Math.min(...others) : Math.max(...others);
		const bv = values[best]!;
		if (Math.abs(bv - runnerUp) <= 0.01 * Math.max(Math.abs(bv), Math.abs(runnerUp))) return -1;
		return best;
	}

	function turnMetrics(t: Turn): (Metrics | null)[] {
		return t.replies.map((r) => (r?.request && r.request.status === 'ok' ? r.request.metrics : null));
	}

	/** Bar length for a value, relative to the largest in its row. */
	function barPct(v: number | null, values: (number | null)[]): number {
		const top = Math.max(0, ...values.filter((x): x is number => x != null));
		return v == null || top <= 0 ? 0 : Math.max(2, (100 * v) / top);
	}

	// Scoreboard across all turns: wins per column, averages, token split.
	interface Score {
		wins: number;
		turns: number;
		ttft: number | null;
		e2e: number | null;
		speed: number | null;
		answerTokens: number;
		reasoningTokens: number;
	}
	const avg = (xs: number[]) => (xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : null);
	const scores = $derived.by((): Score[] => {
		const base = columns.map(() => ({ wins: 0, turns: 0, ttft: [] as number[], e2e: [] as number[], speed: [] as number[], answer: 0, reasoning: 0 }));
		for (const t of turns) {
			const tm = turnMetrics(t);
			tm.forEach((m, i) => {
				if (!m) return;
				const b = base[i];
				b.turns++;
				if (m.ttft_ms != null) b.ttft.push(m.ttft_ms);
				b.e2e.push(m.e2e_ms);
				if (m.output_tps != null) b.speed.push(m.output_tps);
				b.reasoning += m.reasoning_tokens;
				b.answer += Math.max(0, m.output_tokens - m.reasoning_tokens);
			});
			if (tm.filter(Boolean).length < 2) continue;
			for (const [, get, , lower] of summaryRows) {
				const w = bestIndex(tm.map((m) => (m ? get(m) : null)), lower);
				if (w >= 0) base[w].wins++;
			}
		}
		return base.map((b) => ({
			wins: b.wins,
			turns: b.turns,
			ttft: avg(b.ttft),
			e2e: avg(b.e2e),
			speed: avg(b.speed),
			answerTokens: b.answer,
			reasoningTokens: b.reasoning
		}));
	});
	const leader = $derived.by(() => {
		const top = Math.max(0, ...scores.map((s) => s.wins));
		const idx = scores.findIndex((s) => s.wins === top);
		// Only name a leader when it is unambiguous.
		return top > 0 && scores.filter((s) => s.wins === top).length === 1 ? idx : -1;
	});
	const showScoreboard = $derived(turns.some((t) => turnMetrics(t).filter(Boolean).length > 1));
	const maxTokens = $derived(Math.max(1, ...scores.map((s) => s.answerTokens + s.reasoningTokens)));
</script>

<div class="flex h-full">
	<!-- Comparisons -->
	<aside class="flex w-64 shrink-0 flex-col border-r border-stone-200 bg-white">
		<div class="p-3">
			<button class="w-full rounded-md border border-stone-300 px-3 py-1.5 text-sm font-medium hover:bg-stone-50" onclick={newComparison}>
				+ New comparison
			</button>
		</div>
		<ul class="flex-1 overflow-y-auto px-2 pb-3 text-sm">
			{#each compares as g (g.id)}
				<li class="group relative">
					<a href="/compare?c={g.id}" class="block rounded-md px-3 py-2 pr-8 {g.id === compareId ? 'bg-stone-100 text-stone-900' : 'text-stone-700 hover:bg-stone-50'}">
						<div class="truncate">{g.title || 'Untitled'}</div>
						<div class="truncate text-xs text-stone-400">{g.sessions.length} models · {relativeTime(g.updated_at)}</div>
					</a>
					<button class="absolute top-2 right-1 hidden rounded px-1.5 text-stone-400 group-hover:block hover:bg-stone-200 hover:text-stone-700" aria-label="Delete comparison" onclick={() => removeCompare(g)}>✕</button>
				</li>
			{:else}
				<li class="px-3 py-2 text-stone-400">No comparisons yet.</li>
			{/each}
		</ul>
	</aside>

	<section class="flex min-w-0 flex-1 flex-col">
		<!-- Column headers / setup -->
		<div class="grid gap-3 border-b border-stone-200 bg-white p-3" style="grid-template-columns: repeat({Math.max(columns.length, 1)}, minmax(0, 1fr))">
			{#each columns as c, i (c.key)}
				<div class="min-w-0 rounded-md border border-stone-200 p-2.5" style="border-top: 3px solid {seriesColor(i)}">
					{#if locked}
						<div class="truncate font-medium">{columnTitle(c)}</div>
						<div class="truncate text-xs text-stone-500">{columnSubtitle(c)}</div>
					{:else}
						<div class="mb-1.5 flex items-center justify-between gap-2">
							<span class="text-xs font-medium text-stone-500">Model {i + 1}</span>
							{#if columns.length > MIN_COLUMNS}
								<button class="text-xs text-stone-400 hover:text-stone-700" aria-label="Remove column" onclick={() => columns.splice(i, 1)}>✕</button>
							{/if}
						</div>
						{#if profiles.length}
							<select class="mb-1.5 w-full rounded-md border border-stone-300 bg-white px-2 py-1 text-sm" value={c.profileId} onchange={(e) => applyProfile(c, e.currentTarget.value)} aria-label="Profile">
								<option value="">No profile</option>
								{#each profiles as p (p.id)}<option value={p.id}>{p.name}</option>{/each}
							</select>
						{/if}
						{#if !c.profileId}
							<div class="flex gap-1.5">
								<select class="w-1/2 rounded-md border border-stone-300 bg-white px-2 py-1 text-sm" value={c.sourceId} onchange={(e) => changeSource(c, e.currentTarget.value)} aria-label="Source">
									{#each sources as s (s.id)}<option value={s.id}>{s.name}</option>{/each}
								</select>
								<input class="w-1/2 rounded-md border border-stone-300 px-2 py-1 font-mono text-sm" list="cmp-models-{c.key}" bind:value={c.model} aria-label="Model" />
								<datalist id="cmp-models-{c.key}">
									{#each modelOptions[c.sourceId] ?? [] as m (m)}<option value={m}></option>{/each}
								</datalist>
							</div>
						{:else}
							<div class="truncate text-xs text-stone-500">{columnSubtitle(c)}</div>
						{/if}
						<button class="mt-1.5 text-xs text-blue-700 hover:underline" onclick={() => (c.showParams = !c.showParams)}>
							{c.showParams ? 'Hide parameters' : 'Parameters'}{#if c.params.thinking} · {thinkingLabel(c.params)}{/if}
						</button>
						{#if c.showParams}
							<div class="mt-2 border-t border-stone-100 pt-2">
								<ParamsEditor bind:params={c.params} bind:error={c.paramsError} baseUrl={sourceById.get(c.sourceId)?.base_url} />
							</div>
						{/if}
					{/if}
				</div>
			{/each}
			{#if columns.length === 0}
				<p class="text-sm text-stone-500">
					{sources.length ? 'Loading…' : 'Add a source first.'}
					{#if !sources.length}<a href="/sources" class="text-blue-700 underline">Sources</a>{/if}
				</p>
			{/if}
		</div>
		{#if !locked && columns.length > 0 && columns.length < MAX_COLUMNS}
			<div class="border-b border-stone-200 bg-white px-3 pb-2">
				<button class="text-xs text-blue-700 hover:underline" onclick={() => { const c = newColumn({ sourceId: columns.at(-1)?.sourceId }); columns.push(c); loadModels(c.sourceId); }}>
					+ Add model ({columns.length}/{MAX_COLUMNS})
				</button>
			</div>
		{/if}

		<!-- Turns -->
		<div bind:this={scroller} class="min-h-0 flex-1 overflow-y-auto px-3 py-4">
			{#if showScoreboard}
				<!-- Scoreboard: who wins across all turns -->
				<div class="mb-5 grid gap-3" style="grid-template-columns: repeat({columns.length}, minmax(0, 1fr))" aria-label="Scoreboard">
					{#each columns as c, i (c.key)}
						{@const s = scores[i]}
						<div class="min-w-0 rounded-lg border bg-white p-3 {i === leader ? 'border-green-600' : 'border-stone-200'}" style="border-top: 3px solid {seriesColor(i)}">
							<div class="flex items-center justify-between gap-2">
								<span class="truncate text-sm font-medium">{columnTitle(c)}</span>
								{#if i === leader}<span class="shrink-0 rounded bg-green-50 px-1.5 py-0.5 text-[11px] font-medium text-green-800">★ leading</span>{/if}
							</div>
							<div class="mt-1 text-2xl font-semibold tracking-tight">{s.wins} <span class="text-sm font-normal text-stone-500">wins</span></div>
							<p class="text-[11px] text-stone-500">best value in {s.wins} metric comparison{s.wins === 1 ? '' : 's'} over {turns.length} turn{turns.length === 1 ? '' : 's'}</p>
							<dl class="mt-2 grid grid-cols-3 gap-1 text-xs">
								<div><dt class="text-stone-400">avg TTFT</dt><dd class="font-mono">{ms(s.ttft)}</dd></div>
								<div><dt class="text-stone-400">avg E2E</dt><dd class="font-mono">{ms(s.e2e)}</dd></div>
								<div><dt class="text-stone-400">avg speed</dt><dd class="font-mono">{rate(s.speed, 't/s')}</dd></div>
							</dl>
							<!-- Output tokens split into reasoning and answer (part-to-whole stacked bar). -->
							<div class="mt-2">
								<div class="flex h-2 overflow-hidden rounded-sm bg-stone-100" style="width: {(100 * (s.answerTokens + s.reasoningTokens)) / maxTokens}%" role="img" aria-label="{s.reasoningTokens} reasoning and {s.answerTokens} answer tokens">
									{#if s.reasoningTokens}<div style="width: {(100 * s.reasoningTokens) / (s.answerTokens + s.reasoningTokens)}%; background: {seriesColor(1)}" class="mr-[2px]"></div>{/if}
									<div class="flex-1" style="background: {seriesColor(2)}"></div>
								</div>
								<div class="mt-0.5 text-[11px] text-stone-500">
									{int(s.reasoningTokens + s.answerTokens)} output tokens{#if s.reasoningTokens} · {int(s.reasoningTokens)} reasoning{/if}
								</div>
							</div>
						</div>
					{/each}
				</div>
				<div class="mb-5 flex gap-4 text-[11px] text-stone-500">
					<span><span class="mr-1 inline-block h-2 w-2 rounded-sm align-middle" style="background: {seriesColor(1)}"></span>reasoning tokens</span>
					<span><span class="mr-1 inline-block h-2 w-2 rounded-sm align-middle" style="background: {seriesColor(2)}"></span>answer tokens</span>
				</div>
			{/if}
			{#each turns as t, ti (ti)}
				<div class="mb-6">
					<div class="mx-auto mb-3 max-w-3xl rounded-lg bg-blue-50 px-4 py-2.5 text-stone-900"><UserPrompt content={t.prompt} syntheticTokens={t.promptSynthetic} /></div>
					<div class="grid gap-3" style="grid-template-columns: repeat({columns.length}, minmax(0, 1fr))">
						{#each columns as c, i (c.key)}
							{@const r = t.replies[i]}
							<div class="min-w-0 rounded-md border border-stone-200 bg-white p-3" style="border-top: 3px solid {seriesColor(i)}">
								<div class="mb-1 truncate text-xs text-stone-400">{columnTitle(c)}</div>
								{#if r}
									{#if r.reasoning}
										<details class="mb-2 rounded border border-stone-200" open={r.pending && !r.content}>
											<summary class="cursor-pointer px-2 py-1 text-xs text-stone-500">Reasoning</summary>
											<div class="border-t border-stone-100 px-2 py-1.5"><Markdown source={r.reasoning} class="prose-sm text-stone-600" /></div>
										</details>
									{/if}
									{#if r.content}
										<Markdown source={r.content} class="prose-sm" />
									{:else if r.pending && !r.reasoning}
										<div class="animate-pulse text-stone-400">●●●</div>
									{/if}
									{#if r.request}
										{#if r.request.error_message && r.request.status !== 'canceled'}
											<div class="mt-1 rounded bg-red-50 px-2 py-1 text-xs text-red-800">⚠ {r.request.error_message}</div>
										{/if}
										<MetricsStrip request={r.request} onclick={() => (detailId = r.request_id)} />
									{/if}
								{:else if ti === turns.length - 1 && c.error}
									<div class="rounded bg-red-50 px-2 py-1 text-xs text-red-800">⚠ {c.error}</div>
								{:else if streaming && sequential}
									<div class="text-xs text-stone-400">Waiting for its turn…</div>
								{/if}
							</div>
						{/each}
					</div>

					<!-- Per-turn summary with the best value highlighted -->
					{#if turnMetrics(t).filter(Boolean).length > 1}
						{@const tm = turnMetrics(t)}
						<table class="mt-2 w-full text-right text-xs">
							<thead>
								<tr class="text-stone-500">
									<th class="py-1 pr-2 text-left font-normal"></th>
									{#each columns as c, i (c.key)}
										<th class="px-2 py-1 font-medium whitespace-nowrap">
											<span class="mr-1 inline-block h-2 w-2 rounded-sm align-middle" style="background: {seriesColor(i)}" aria-hidden="true"></span>{columnTitle(c)}
										</th>
									{/each}
								</tr>
							</thead>
							<tbody>
								{#each summaryRows as [label, get, fmt, lower] (label)}
									{@const values = tm.map((m) => (m ? get(m) : null))}
									{@const best = bestIndex(values, lower)}
									<tr class="border-t border-stone-100">
										<td class="py-1 pr-2 text-left whitespace-nowrap text-stone-500">{label}</td>
										{#each values as v, i (i)}
											<td class="px-2 py-1 font-mono {i === best ? 'font-semibold text-stone-900' : 'text-stone-600'}">
												{fmt(v)}{#if i === best}<span class="ml-1 font-sans text-[10px] font-normal text-green-800">★ best</span>{/if}
												<!-- Inline bar: length relative to the row's largest value. -->
												<div class="mt-0.5 ml-auto h-1.5 rounded-sm" style="width: {barPct(v, values)}%; background: {seriesColor(i)}; opacity: {i === best || best < 0 ? 1 : 0.55}" aria-hidden="true"></div>
											</td>
										{/each}
									</tr>
								{/each}
							</tbody>
						</table>
					{/if}
				</div>
			{:else}
				{#if columns.length}
					<p class="mt-16 text-center text-sm text-stone-400">
						Pick 2–{MAX_COLUMNS} models or profiles above, then send a prompt: each one answers side by side, measured.
					</p>
				{/if}
			{/each}
		</div>

		{#if error}
			<div class="mx-3 mb-2 flex items-start justify-between gap-3 rounded-md bg-red-50 px-3 py-2 text-sm text-red-800">
				<span>⚠ {error}</span>
				<button class="text-red-600" aria-label="Dismiss" onclick={() => (error = '')}>✕</button>
			</div>
		{/if}

		<div class="border-t border-stone-200 bg-white p-3">
			<div class="mx-auto mb-2 max-w-4xl"><SyntheticControl bind:enabled={synthetic} bind:tokens={synthTokens} disabled={streaming} /></div>
			<div class="mx-auto flex max-w-4xl items-end gap-2">
				<textarea
					class="max-h-48 min-h-10 flex-1 resize-y rounded-md border border-stone-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none"
					rows="2"
					placeholder={synthetic ? `Optional instruction after the ${synthTokens}-token synthetic text (Enter to send)` : "Prompt for all models (Enter to send)"}
					bind:value={input}
					onkeydown={onKeydown}
					disabled={columns.length < MIN_COLUMNS}
				></textarea>
				<div class="flex flex-col items-end gap-1">
					<label class="flex items-center gap-1 text-xs text-stone-500" title="Send to one model at a time so they don't compete for the same server">
						<input type="checkbox" bind:checked={sequential} onchange={() => (sequentialTouched = true)} disabled={streaming} />
						One at a time
					</label>
					{#if streaming}
						<button class="rounded-md bg-stone-800 px-4 py-2 text-sm font-medium text-white hover:bg-stone-700" onclick={() => abort?.abort()}>Stop</button>
					{:else}
						<button class="rounded-md bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-40" disabled={(!input.trim() && !synthetic) || columns.length < MIN_COLUMNS} onclick={sendAll}>
							Send to all
						</button>
					{/if}
				</div>
			</div>
		</div>
	</section>
</div>

{#if detailId}
	<RequestDetail requestId={detailId} onclose={() => (detailId = null)} />
{/if}
