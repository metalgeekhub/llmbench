<script lang="ts">
	import { getRequest } from '$lib/api';
	import { dateTime, int, ms, rate } from '$lib/format';
	import type { RequestRecord, Timeline } from '$lib/types';
	import TimelineChart from './TimelineChart.svelte';

	let { requestId, onclose }: { requestId: string; onclose: () => void } = $props();

	let request = $state<RequestRecord>();
	let timeline = $state<Timeline | null>(null);
	let error = $state('');

	$effect(() => {
		const id = requestId;
		request = undefined;
		error = '';
		getRequest(id)
			.then((r) => {
				if (id !== requestId) return;
				request = r.request;
				timeline = r.timeline;
			})
			.catch((e) => (error = e.message));
	});

	const m = $derived(request?.metrics);
	const statusLabel: Record<string, string> = { ok: '✓ OK', error: '⚠ Failed', canceled: '⏹ Canceled' };
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && onclose()} />

<div
	class="fixed inset-0 z-40 flex justify-end bg-black/20"
	role="presentation"
	onclick={(e) => e.target === e.currentTarget && onclose()}
>
	<aside class="h-full w-full max-w-2xl overflow-y-auto bg-white p-6 shadow-xl" aria-label="Request details">
		<div class="mb-4 flex items-start justify-between gap-4">
			<div>
				<h2 class="text-lg font-semibold text-stone-900">Request details</h2>
				{#if request}
					<p class="text-sm text-stone-500">
						{request.source_name} · <span class="font-mono">{request.model}</span> · {dateTime(
							request.started_at
						)}
					</p>
				{/if}
			</div>
			<button class="rounded px-2 py-1 text-stone-500 hover:bg-stone-100" onclick={onclose} aria-label="Close">
				✕
			</button>
		</div>

		{#if error}
			<p class="text-sm text-red-700">{error}</p>
		{:else if !request || !m}
			<p class="text-sm text-stone-500">Loading…</p>
		{:else}
			<div class="mb-4 flex flex-wrap items-center gap-2 text-sm">
				<span
					class="rounded px-2 py-0.5 font-medium {request.status === 'ok'
						? 'bg-green-50 text-green-800'
						: 'bg-red-50 text-red-800'}"
				>
					{statusLabel[request.status] ?? request.status}
				</span>
				{#if request.http_status}<span class="text-stone-600">HTTP {request.http_status}</span>{/if}
				{#if request.error_type}<span class="font-mono text-stone-600">{request.error_type}</span>{/if}
			</div>
			{#if request.error_message}
				<pre class="mb-4 rounded bg-red-50 p-3 text-xs whitespace-pre-wrap text-red-900">{request.error_message}</pre>
			{/if}

			<h3 class="mb-2 text-sm font-semibold text-stone-700">Timing</h3>
			<dl class="mb-5 grid grid-cols-2 gap-x-6 gap-y-2 text-sm sm:grid-cols-3">
				{#each [
					['Time to first token', ms(m.ttft_ms), 'Request sent → first streamed token (reasoning or answer)'],
					['Time to first answer', ms(m.ttfat_ms), 'Request sent → first non-reasoning token'],
					['End-to-end latency', ms(m.e2e_ms), 'Request sent → final chunk received'],
					['Time per output token', ms(m.tpot_ms), '(E2E − TTFT) / (output tokens − 1)'],
					['Output speed', rate(m.output_tps), 'Output tokens / (E2E − TTFT)'],
					['Prefill speed', rate(m.prefill_tps), 'Input tokens / TTFT (approximate)']
				] as [label, value, hint] (label)}
					<div title={hint}>
						<dt class="text-xs text-stone-500">{label}</dt>
						<dd class="font-mono text-stone-900">{value}</dd>
					</div>
				{/each}
			</dl>

			<h3 class="mb-2 text-sm font-semibold text-stone-700">
				Tokens
				{#if m.tokens_estimated}
					<span class="ml-1 text-xs font-normal text-amber-700">estimated with tokenizer (server did not report usage)</span>
				{/if}
			</h3>
			<dl class="mb-5 grid grid-cols-2 gap-x-6 gap-y-2 text-sm sm:grid-cols-4">
				{#each [
					['Input', m.input_tokens],
					['Output', m.output_tokens],
					['Reasoning', m.reasoning_tokens],
					['Cached', m.cached_tokens]
				] as [label, value] (label)}
					<div>
						<dt class="text-xs text-stone-500">{label}</dt>
						<dd class="font-mono text-stone-900">{int(value as number)}</dd>
					</div>
				{/each}
			</dl>

			<h3 class="mb-1 text-sm font-semibold text-stone-700">Inter-chunk latency</h3>
			<p class="mb-2 text-xs text-stone-500">
				Gaps between {int(m.chunk_count)} streamed chunks. Measured per chunk: servers may send several tokens per chunk.
			</p>
			{#if m.itl.count > 0}
				<table class="mb-5 w-full text-right font-mono text-sm">
					<thead class="text-xs text-stone-500">
						<tr>
							{#each ['min', 'mean', 'p50', 'p90', 'p95', 'p99', 'max'] as h (h)}
								<th class="px-1 font-normal">{h}</th>
							{/each}
						</tr>
					</thead>
					<tbody>
						<tr>
							{#each [m.itl.min, m.itl.mean, m.itl.p50, m.itl.p90, m.itl.p95, m.itl.p99, m.itl.max] as v, i (i)}
								<td class="px-1">{ms(v)}</td>
							{/each}
						</tr>
					</tbody>
				</table>
			{:else}
				<p class="mb-5 text-sm text-stone-500">Not enough chunks.</p>
			{/if}

			{#if timeline && timeline.t.length > 0}
				<h3 class="mb-1 text-sm font-semibold text-stone-700">Token timeline</h3>
				<p class="mb-2 text-xs text-stone-500">
					Cumulative chunks received over time. Steeper is faster; flat segments are stalls.
				</p>
				<TimelineChart {timeline} />
			{/if}

			{#if request.params && Object.keys(request.params).length}
				<h3 class="mt-5 mb-2 text-sm font-semibold text-stone-700">Parameters</h3>
				<pre class="rounded bg-stone-50 p-3 text-xs text-stone-700">{JSON.stringify(request.params, null, 2)}</pre>
			{/if}
			<p class="mt-5 text-xs text-stone-400">
				Latency includes the network path between LLMBench and the endpoint. Request ID {request.id}
			</p>
		{/if}
	</aside>
</div>
