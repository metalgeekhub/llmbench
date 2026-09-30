<script lang="ts">
	import { rampColor, SEQUENTIAL } from '$lib/palette';

	let {
		xLabels,
		yLabels,
		xTitle,
		yTitle,
		values,
		format,
		lowerIsBetter
	}: {
		xLabels: string[];
		yLabels: string[];
		xTitle: string;
		yTitle: string;
		/** values[y][x]; null = not measured. */
		values: (number | null)[][];
		format: (v: number) => string;
		lowerIsBetter: boolean;
	} = $props();

	const flat = $derived(values.flat().filter((v): v is number => v != null));
	const min = $derived(flat.length ? Math.min(...flat) : 0);
	const max = $derived(flat.length ? Math.max(...flat) : 0);
	// Sequential: darker = larger value, whatever "better" means for the metric.
	const t = (v: number) => (max > min ? (v - min) / (max - min) : 0.5);
	const best = $derived(flat.length ? (lowerIsBetter ? min : max) : null);
</script>

<div class="overflow-x-auto">
	<div class="inline-grid gap-[2px] text-xs" style="grid-template-columns: auto repeat({xLabels.length}, minmax(4.5rem, 1fr))">
		<div class="px-2 py-1 text-right text-stone-400">{yTitle} ↓ · {xTitle} →</div>
		{#each xLabels as x (x)}<div class="px-2 py-1 text-center font-medium text-stone-600">{x}</div>{/each}
		{#each yLabels as y, yi (y)}
			<div class="px-2 py-2 text-right font-medium whitespace-nowrap text-stone-600">{y}</div>
			{#each xLabels as x, xi (x)}
				{@const v = values[yi]?.[xi] ?? null}
				{#if v == null}
					<div class="rounded bg-stone-100 px-2 py-2 text-center text-stone-400" title="{y} · {x}: not measured">–</div>
				{:else}
					{@const c = rampColor(t(v))}
					<div
						class="rounded px-2 py-2 text-center font-mono {c.whiteText ? 'text-white' : 'text-stone-900'} {v === best ? 'font-semibold' : ''}"
						style="background: {c.color}"
						title="{y} · {x}: {format(v)}"
					>
						{format(v)}{#if v === best}<span class="ml-0.5 font-sans">★</span>{/if}
					</div>
				{/if}
			{/each}
		{/each}
	</div>
</div>
<div class="mt-2 flex items-center gap-2 text-[11px] text-stone-500">
	<span>{flat.length ? format(min) : ''}</span>
	<span class="h-2 w-40 rounded" style="background: linear-gradient(to right, {SEQUENTIAL.join(', ')})" aria-hidden="true"></span>
	<span>{flat.length ? format(max) : ''}</span>
	<span class="ml-2">Darker = higher. ★ = best ({lowerIsBetter ? 'lowest' : 'highest'}).</span>
</div>
