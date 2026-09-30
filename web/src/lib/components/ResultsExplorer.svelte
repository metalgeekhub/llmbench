<script lang="ts">
	import { untrack } from 'svelte';
	import {
		DIM_LABELS,
		dimValue,
		dimValueLabel,
		dimValues,
		formatTokens,
		metricByKey,
		metricsFor,
		type Dim,
		type ExplorerCell
	} from '$lib/dims';
	import { seriesColor } from '$lib/palette';
	import type { DimSeries } from '$lib/charts';
	import DimChart from './DimChart.svelte';

	export interface ExplorerSettings {
		x?: Dim;
		series?: Dim | '';
		filters?: Partial<Record<Dim, string>>;
		metrics?: [string, string];
	}

	let {
		cells,
		settings = $bindable({}),
		showDelta = false,
		entityNoun = 'Model',
		defaultMetrics = ['output_throughput', 'ttft_p95']
	}: {
		cells: ExplorerCell[];
		/** Chart state, bindable so a comparison can be saved and restored. */
		settings?: ExplorerSettings;
		/** Show % difference against the first series (comparison view). */
		showDelta?: boolean;
		entityNoun?: string;
		/** Metrics shown when settings don't specify any. */
		defaultMetrics?: [string, string];
	} = $props();

	const ALL_DIMS: Dim[] = ['load', 'context', 'thinking', 'entity'];

	const varying = $derived(ALL_DIMS.filter((d) => dimValues(cells, d).length > 1));
	const loadKind = $derived(cells[0]?.loadKind ?? 'users');

	const initial = untrack(() => settings);
	let x = $state<Dim>(initial.x ?? 'load');
	let seriesDim = $state<Dim | ''>(initial.series ?? 'entity');
	let filters = $state<Partial<Record<Dim, string>>>({ ...(initial.filters ?? {}) });
	let metricA = $state(initial.metrics?.[0] ?? untrack(() => defaultMetrics[0]));
	let metricB = $state(initial.metrics?.[1] ?? untrack(() => defaultMetrics[1]));
	let tableA = $state(untrack(() => showDelta));
	let tableB = $state(untrack(() => showDelta));

	const xOptions = $derived.by((): Dim[] => {
		const opts = varying.filter((d) => d !== 'entity');
		return opts.length ? opts : ['entity'];
	});
	const seriesOptions = $derived(varying.filter((d) => d !== x));

	// Preferred dimension for lines when the current choice isn't available.
	const SERIES_PREFERENCE: Dim[] = ['entity', 'thinking', 'context', 'load'];

	// Keep choices valid as the data (e.g. a live run) changes.
	$effect(() => {
		if (!xOptions.includes(x)) x = xOptions[0];
	});
	$effect(() => {
		if (seriesDim && seriesOptions.includes(seriesDim)) return;
		seriesDim = SERIES_PREFERENCE.find((d) => seriesOptions.includes(d)) ?? '';
	});

	const filterDims = $derived(varying.filter((d) => d !== x && d !== seriesDim));
	$effect(() => {
		for (const d of filterDims) {
			const vals = dimValues(cells, d).map(String);
			if (!filters[d] || !vals.includes(filters[d]!)) filters[d] = vals[0];
		}
	});

	// Write back for saving.
	$effect(() => {
		settings = { x, series: seriesDim, filters: { ...filters }, metrics: [metricA, metricB] };
	});

	const filtered = $derived(cells.filter((c) => filterDims.every((d) => String(dimValue(c, d)) === filters[d])));

	function dimName(d: Dim): string {
		if (d === 'entity') return entityNoun;
		if (d === 'load') return loadKind === 'rate' ? 'Requests per second' : 'Concurrent users';
		return DIM_LABELS[d];
	}

	function entityLabel(key: string): string {
		return cells.find((c) => c.entity === key)?.entityLabel ?? key;
	}
	function valueLabel(dim: Dim, v: string | number): string {
		return dim === 'entity' ? entityLabel(String(v)) : dimValueLabel(dim, v, loadKind);
	}
	function xFormat(v: string | number): string {
		if (x === 'context') return formatTokens(Number(v));
		if (x === 'entity') return entityLabel(String(v));
		return String(v);
	}

	const xVals = $derived(dimValues(filtered, x));
	const seriesVals = $derived(seriesDim ? dimValues(filtered, seriesDim) : ['']);

	function seriesFor(metricKey: string): DimSeries[] {
		const m = metricByKey(metricKey);
		return seriesVals.map((sv, i) => {
			const members = seriesDim ? filtered.filter((c) => String(dimValue(c, seriesDim as Dim)) === String(sv)) : filtered;
			const color =
				seriesDim === 'entity'
					? seriesColor(members[0]?.entityIndex ?? i)
					: seriesColor(i);
			return {
				name: seriesDim ? valueLabel(seriesDim, sv) : m.label,
				color,
				data: xVals.map((xv) => {
					const cell = members.find((c) => String(dimValue(c, x)) === String(xv));
					return [x === 'entity' || x === 'thinking' ? String(xv) : Number(xv), cell ? m.get(cell.summary) : null];
				})
			};
		});
	}

	const categories = $derived(x === 'thinking' || x === 'entity' ? xVals.map(String) : []);
	const seriesA = $derived(seriesFor(metricA));
	const seriesB = $derived(seriesFor(metricB));

	function best(values: (number | null)[], lowerIsBetter: boolean): number {
		let bi = -1;
		values.forEach((v, i) => {
			if (v == null) return;
			if (bi < 0 || (lowerIsBetter ? v < values[bi]! : v > values[bi]!)) bi = i;
		});
		return values.filter((v) => v != null).length > 1 ? bi : -1;
	}

	function delta(v: number | null, base: number | null): string {
		if (v == null || base == null || base === 0) return '';
		const d = (100 * (v - base)) / base;
		if (Math.abs(d) < 0.5) return '±0%';
		return `${d > 0 ? '+' : '−'}${Math.abs(d).toFixed(0)}%`;
	}

	const selectClass = 'rounded-md border border-stone-300 bg-white px-2 py-1 text-xs';
</script>

{#if cells.length === 0}
	<p class="text-sm text-stone-500">No measured steps yet.</p>
{:else}
	<div class="mb-4 flex flex-wrap items-center gap-3 text-xs">
		<label class="flex items-center gap-1.5">
			<span class="text-stone-500">X axis</span>
			<select class={selectClass} bind:value={x}>
				{#each xOptions as d (d)}<option value={d}>{dimName(d)}</option>{/each}
			</select>
		</label>
		{#if seriesOptions.length}
			<label class="flex items-center gap-1.5">
				<span class="text-stone-500">Lines</span>
				<select class={selectClass} bind:value={seriesDim}>
					{#each seriesOptions as d (d)}<option value={d}>one per {dimName(d).toLowerCase()}</option>{/each}
				</select>
			</label>
		{/if}
		{#each filterDims as d (d)}
			<label class="flex items-center gap-1.5">
				<span class="text-stone-500">{dimName(d)}</span>
				<select class={selectClass} bind:value={filters[d]}>
					{#each dimValues(cells, d) as v (v)}<option value={String(v)}>{valueLabel(d, v)}</option>{/each}
				</select>
			</label>
		{/each}
	</div>

	<div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
		{#each [{ key: metricA, set: (v: string) => (metricA = v), series: seriesA, table: tableA, toggle: () => (tableA = !tableA) }, { key: metricB, set: (v: string) => (metricB = v), series: seriesB, table: tableB, toggle: () => (tableB = !tableB) }] as panel, pi (pi)}
			{@const m = metricByKey(panel.key)}
			<div class="min-w-0">
				<div class="mb-1 flex items-center justify-between gap-2">
					<select class="{selectClass} font-medium" value={panel.key} onchange={(e) => panel.set(e.currentTarget.value)} aria-label="Metric">
						{#each metricsFor(cells) as mt (mt.key)}<option value={mt.key}>{mt.label}</option>{/each}
					</select>
					<button class="text-xs text-blue-700 hover:underline" onclick={panel.toggle}>{panel.table ? 'Hide table' : 'Table'}</button>
				</div>
				<p class="mb-1 text-xs text-stone-400">{m.lowerIsBetter ? 'Lower is better.' : 'Higher is better.'}</p>
				<DimChart
					series={panel.series}
					xLabel={dimName(x)}
					{categories}
					format={(v) => m.fmt(v)}
					{xFormat}
					ariaLabel="{m.label} by {dimName(x)}"
				/>
				{#if panel.table}
					<div class="mt-2 overflow-x-auto">
						<table class="w-full text-right text-xs">
							<thead class="text-stone-500">
								<tr>
									<th class="py-1 pr-2 text-left font-medium">{dimName(x)}</th>
									{#each panel.series as s (s.name)}
										<th class="px-2 py-1 font-medium whitespace-nowrap">
											<span class="mr-1 inline-block h-2 w-2 rounded-sm align-middle" style="background: {s.color}" aria-hidden="true"></span>{s.name}
										</th>
									{/each}
								</tr>
							</thead>
							<tbody>
								{#each xVals as xv, xi (xv)}
									{@const values = panel.series.map((s) => s.data[xi]?.[1] ?? null)}
									{@const bi = best(values, m.lowerIsBetter)}
									<tr class="border-t border-stone-100">
										<td class="py-1 pr-2 text-left whitespace-nowrap text-stone-600">{valueLabel(x, xv)}</td>
										{#each values as v, i (i)}
											<td class="px-2 py-1 font-mono whitespace-nowrap {i === bi ? 'font-semibold text-stone-900' : 'text-stone-600'}">
												{m.fmt(v)}{#if i === bi}<span class="ml-1 font-sans text-[10px] font-normal text-green-800">★</span>{/if}
												{#if showDelta && i > 0 && delta(v, values[0])}
													<span class="ml-1 font-sans text-[10px] text-stone-400">{delta(v, values[0])}</span>
												{/if}
											</td>
										{/each}
									</tr>
								{/each}
							</tbody>
						</table>
						{#if showDelta && panel.series.length > 1}
							<p class="mt-1 text-[11px] text-stone-400">Percentages compare each column with the first ({panel.series[0].name}).</p>
						{/if}
					</div>
				{/if}
			</div>
		{/each}
	</div>
{/if}
