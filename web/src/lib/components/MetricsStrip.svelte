<script lang="ts">
	import type { RequestRecord } from '$lib/types';
	import { int, ms, rate } from '$lib/format';

	let { request, onclick }: { request: RequestRecord; onclick?: () => void } = $props();

	const m = $derived(request.metrics);
	const failed = $derived(request.status !== 'ok');
</script>

<button
	type="button"
	class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 rounded px-1.5 py-0.5 text-left font-mono text-[11px] text-stone-500 hover:bg-stone-100 hover:text-stone-700"
	title="Show timing details"
	{onclick}
>
	{#if failed}
		<span class="font-sans font-medium text-red-700">
			{request.status === 'canceled' ? '⏹ Canceled' : '⚠ Failed'}
			{#if request.http_status}· HTTP {request.http_status}{/if}
		</span>
	{/if}
	<span><span class="text-stone-400">TTFT</span> {ms(m.ttft_ms)}</span>
	<span><span class="text-stone-400">E2E</span> {ms(m.e2e_ms)}</span>
	<span><span class="text-stone-400">TPOT</span> {ms(m.tpot_ms)}</span>
	<span>{rate(m.output_tps)}</span>
	<span>
		<span class="text-stone-400">tokens</span>
		{int(m.input_tokens)} → {int(m.output_tokens)}{#if m.reasoning_tokens}
			<span class="text-stone-400">({int(m.reasoning_tokens)} reasoning)</span>{/if}{#if m.tokens_estimated}
			<span class="text-amber-700" title="Server did not report usage; counted with a tokenizer">~est.</span>{/if}
	</span>
</button>
