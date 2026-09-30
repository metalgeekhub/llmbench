<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { listRequestModels, listRequests, listSources } from '$lib/api';
	import { dateTime, int, ms, rate } from '$lib/format';
	import type { RequestRecord, Source } from '$lib/types';
	import RequestDetail from '$lib/components/RequestDetail.svelte';

	const PAGE_SIZE = 50;

	let sources = $state<Source[]>([]);
	let models = $state<string[]>([]);
	let requests = $state<RequestRecord[]>([]);
	let total = $state(0);
	let loading = $state(true);
	let error = $state('');

	let sourceId = $state('');
	let model = $state('');
	let status = $state('');
	let offset = $state(0);
	// ?r=<id> opens a request's details directly (shareable link).
	let detailId = $state<string | null>(page.url.searchParams.get('r'));

	onMount(() => {
		listSources().then((s) => (sources = s)).catch(() => {});
		listRequestModels().then((m) => (models = m)).catch(() => {});
	});

	// Reload whenever a filter or the page changes.
	$effect(() => {
		const filter = { source_id: sourceId, model, status, offset, limit: PAGE_SIZE };
		loading = true;
		listRequests(filter)
			.then((r) => {
				requests = r.requests;
				total = r.total;
				error = '';
			})
			.catch((e) => (error = e.message))
			.finally(() => (loading = false));
	});

	function setFilter(apply: () => void) {
		apply();
		offset = 0;
	}

	const selectClass = 'rounded-md border border-stone-300 bg-white px-2 py-1 text-sm';
	const statusLabel: Record<string, string> = { ok: '✓ ok', error: '⚠ error', canceled: '⏹ canceled' };
</script>

<div class="flex h-full flex-col p-6">
	<div class="mb-4 flex flex-wrap items-end justify-between gap-4">
		<div>
			<h1 class="text-xl font-semibold">Request history</h1>
			<p class="text-sm text-stone-500">Every request made by LLMBench, with its measured metrics.</p>
		</div>
		<div class="flex flex-wrap gap-2">
			<select class={selectClass} value={sourceId} onchange={(e) => setFilter(() => (sourceId = e.currentTarget.value))} aria-label="Filter by source">
				<option value="">All sources</option>
				{#each sources as s (s.id)}<option value={s.id}>{s.name}</option>{/each}
			</select>
			<select class={selectClass} value={model} onchange={(e) => setFilter(() => (model = e.currentTarget.value))} aria-label="Filter by model">
				<option value="">All models</option>
				{#each models as m (m)}<option value={m}>{m}</option>{/each}
			</select>
			<select class={selectClass} value={status} onchange={(e) => setFilter(() => (status = e.currentTarget.value))} aria-label="Filter by status">
				<option value="">Any status</option>
				<option value="ok">OK</option>
				<option value="error">Error</option>
				<option value="canceled">Canceled</option>
			</select>
		</div>
	</div>

	{#if error}
		<p class="mb-4 rounded bg-red-50 px-3 py-2 text-sm text-red-800">⚠ {error}</p>
	{/if}

	<div class="min-h-0 flex-1 overflow-auto rounded-lg border border-stone-200 bg-white">
		<table class="w-full text-sm">
			<thead class="sticky top-0 bg-stone-50 text-xs text-stone-500">
				<tr class="text-left">
					<th class="px-3 py-2 font-medium">Time</th>
					<th class="px-3 py-2 font-medium">Source</th>
					<th class="px-3 py-2 font-medium">Model</th>
					<th class="px-3 py-2 font-medium">Status</th>
					<th class="px-3 py-2 text-right font-medium">TTFT</th>
					<th class="px-3 py-2 text-right font-medium">E2E</th>
					<th class="px-3 py-2 text-right font-medium">TPOT</th>
					<th class="px-3 py-2 text-right font-medium">Output</th>
					<th class="px-3 py-2 text-right font-medium">Tokens in → out</th>
					<th class="px-3 py-2 font-medium"></th>
				</tr>
			</thead>
			<tbody class="divide-y divide-stone-100">
				{#each requests as r (r.id)}
					<tr class="cursor-pointer hover:bg-stone-50" onclick={() => (detailId = r.id)}>
						<td class="px-3 py-2 whitespace-nowrap text-stone-600">{dateTime(r.started_at)}</td>
						<td class="px-3 py-2">{r.source_name}</td>
						<td class="max-w-56 truncate px-3 py-2 font-mono text-xs" title={r.model}>{r.model}</td>
						<td class="px-3 py-2 whitespace-nowrap {r.status === 'ok' ? 'text-green-800' : 'text-red-800'}" title={r.error_message}>
							{statusLabel[r.status] ?? r.status}{r.http_status ? ` ${r.http_status}` : ''}
						</td>
						<td class="px-3 py-2 text-right font-mono whitespace-nowrap">{ms(r.metrics.ttft_ms)}</td>
						<td class="px-3 py-2 text-right font-mono whitespace-nowrap">{ms(r.metrics.e2e_ms)}</td>
						<td class="px-3 py-2 text-right font-mono whitespace-nowrap">{ms(r.metrics.tpot_ms)}</td>
						<td class="px-3 py-2 text-right font-mono whitespace-nowrap">{rate(r.metrics.output_tps)}</td>
						<td class="px-3 py-2 text-right font-mono whitespace-nowrap">
							{int(r.metrics.input_tokens)} → {int(r.metrics.output_tokens)}{#if r.metrics.tokens_estimated}<span class="text-amber-700" title="Estimated with tokenizer">~</span>{/if}
						</td>
						<td class="px-3 py-2 whitespace-nowrap">
							{#if r.session_id}
								<a href="/chat?s={r.session_id}" class="text-xs text-blue-700 hover:underline" onclick={(e) => e.stopPropagation()}>chat →</a>
							{/if}
						</td>
					</tr>
				{:else}
					<tr>
						<td colspan="10" class="px-3 py-10 text-center text-stone-400">
							{loading ? 'Loading…' : 'No requests yet. Chat with a model to record some.'}
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>

	{#if total > PAGE_SIZE}
		<div class="mt-3 flex items-center justify-end gap-3 text-sm text-stone-600">
			<span>{offset + 1}–{Math.min(offset + PAGE_SIZE, total)} of {total}</span>
			<button class="rounded px-2 py-1 hover:bg-stone-100 disabled:opacity-40" disabled={offset === 0} onclick={() => (offset = Math.max(0, offset - PAGE_SIZE))}>← Newer</button>
			<button class="rounded px-2 py-1 hover:bg-stone-100 disabled:opacity-40" disabled={offset + PAGE_SIZE >= total} onclick={() => (offset += PAGE_SIZE)}>Older →</button>
		</div>
	{/if}
</div>

{#if detailId}
	<RequestDetail requestId={detailId} onclose={() => (detailId = null)} />
{/if}
