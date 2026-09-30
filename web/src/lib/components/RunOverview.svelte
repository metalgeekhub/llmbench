<script lang="ts">
	import { untrack } from 'svelte';
	import { getRunDistribution, type DistributionMetric } from '$lib/api';
	import {
		DIM_LABELS,
		dimValue,
		dimValues,
		formatTokens,
		metricByKey,
		metricsFor,
		type Dim,
		type ExplorerCell
	} from '$lib/dims';
	import { int, ms, rate } from '$lib/format';
	import { errorFinding, findings, goodputFindings, SLO_ATTAINMENT, sloCapacity } from '$lib/insights';
	import { seriesColor, STATUS } from '$lib/palette';
	import type { Run, SLO } from '$lib/types';
	import Donut, { type Slice } from './Donut.svelte';
	import Heatmap from './Heatmap.svelte';
	import Histogram, { type HistGroup } from './Histogram.svelte';
	import Meter from './Meter.svelte';
	import StatTile from './StatTile.svelte';

	let {
		run,
		cells,
		cellDims,
		slo = null
	}: {
		run: Run;
		/** The SLO the cells' goodput was evaluated against, if any. */
		slo?: SLO | null;
		/** Measured cells (at least one successful request). */
		cells: ExplorerCell[];
		/** Describes a cell's dimensions, e.g. "4k tokens · 8 users". */
		cellDims: (c: ExplorerCell) => string;
	} = $props();

	const summaries = $derived(run.cells.flatMap((c) => (c.summary ? [c.summary] : [])));
	const loadKind = $derived(cells[0]?.loadKind ?? 'users');
	const multiModel = $derived(new Set(cells.map((c) => c.entity)).size > 1);
	const where = (c: ExplorerCell) => (multiModel ? `${c.entityLabel} · ${cellDims(c)}` : cellDims(c));

	function bestBy(get: (c: ExplorerCell) => number | null, lower: boolean): ExplorerCell | null {
		let best: ExplorerCell | null = null;
		let bv = 0;
		for (const c of cells) {
			const v = get(c);
			if (v == null) continue;
			if (!best || (lower ? v < bv : v > bv)) {
				best = c;
				bv = v;
			}
		}
		return best;
	}

	// Headline tiles.
	const peak = $derived(bestBy((c) => c.summary.output_throughput, false));
	const fastest = $derived(bestBy((c) => (c.summary.ttft_ms.count ? c.summary.ttft_ms.p50 : null), true));
	const quickest = $derived(bestBy((c) => (c.summary.output_tps.count ? c.summary.output_tps.mean : null), false));
	const totals = $derived.by(() => {
		const t = { ok: 0, failed: 0, canceled: 0, input: 0, output: 0, reasoning: 0 };
		for (const s of summaries) {
			t.ok += s.succeeded;
			t.failed += s.failed;
			t.canceled += s.canceled;
			t.input += s.input_tokens;
			t.output += s.output_tokens;
			t.reasoning += s.reasoning_tokens ?? 0;
		}
		return t;
	});
	const successRatio = $derived(totals.ok + totals.failed ? totals.ok / (totals.ok + totals.failed) : 1);

	// Goodput under the SLO.
	const withGoodput = $derived(cells.filter((c) => c.summary.goodput?.requests));
	const sloRatio = $derived.by(() => {
		const good = withGoodput.reduce((n, c) => n + c.summary.goodput!.good, 0);
		const total = withGoodput.reduce((n, c) => n + c.summary.goodput!.requests, 0);
		return total ? good / total : null;
	});
	const bestGoodput = $derived(bestBy((c) => c.summary.goodput?.per_second ?? null, false));
	// A single load sweep has one capacity number: the highest load within the SLO.
	const capacity = $derived.by(() => {
		const dims = (['entity', 'context', 'thinking'] as Dim[]).filter((d) => dimValues(withGoodput, d).length > 1);
		if (dims.length || dimValues(withGoodput, 'load').length < 2) return undefined;
		return sloCapacity([...withGoodput].sort((a, b) => a.load - b.load));
	});
	const loadText = (c: ExplorerCell) => (c.loadKind === 'rate' ? `${c.load} req/s` : `${c.load} user${c.load === 1 ? '' : 's'}`);

	// Findings.
	const varying = $derived((['entity', 'load', 'context', 'thinking'] as Dim[]).filter((d) => dimValues(cells, d).length > 1));
	const notes = $derived.by(() => {
		const list = [...goodputFindings(cells, varying), ...findings(cells, varying)];
		const err = errorFinding(summaries);
		return err ? [err, ...list] : list;
	});

	// Outcome donut (only when there is more than one outcome).
	const outcomeSlices = $derived.by((): Slice[] => {
		const byType: Record<string, number> = {};
		for (const s of summaries) for (const [k, v] of Object.entries(s.errors_by_type)) byType[k] = (byType[k] ?? 0) + v;
		const errs = Object.entries(byType).sort((a, b) => b[1] - a[1]);
		const errColors = [STATUS.critical, STATUS.serious, STATUS.warning];
		const slices: Slice[] = [{ name: '✓ Succeeded', value: totals.ok, color: STATUS.good }];
		errs.slice(0, 3).forEach(([k, v], i) => slices.push({ name: `⚠ ${k}`, value: v, color: errColors[i] }));
		const rest = errs.slice(3).reduce((n, [, v]) => n + v, 0);
		if (rest) slices.push({ name: '⚠ other errors', value: rest, color: STATUS.critical });
		if (totals.canceled) slices.push({ name: '⏹ Canceled', value: totals.canceled, color: '#a8a29e' });
		return slices;
	});
	const tokenSlices = $derived<Slice[]>([
		{ name: 'Input (prompt)', value: totals.input, color: seriesColor(0) },
		{ name: 'Reasoning', value: totals.reasoning, color: seriesColor(1) },
		{ name: 'Answer', value: Math.max(0, totals.output - totals.reasoning), color: seriesColor(2) }
	]);

	// Latency distribution.
	const distMetrics: { key: DistributionMetric; label: string; fmt: (v: number) => string }[] = [
		{ key: 'ttft_ms', label: 'Time to first token', fmt: (v) => ms(v) },
		{ key: 'e2e_ms', label: 'End-to-end latency', fmt: (v) => ms(v) },
		{ key: 'tpot_ms', label: 'Time per output token', fmt: (v) => ms(v) },
		{ key: 'output_tps', label: 'Output speed per user', fmt: (v) => rate(v, '') }
	];
	// Thinking changes when the answer starts and ends, not TTFT.
	let distMetric = $state<DistributionMetric>(untrack(() => (run.type === 'thinking_comparison' ? 'e2e_ms' : 'ttft_ms')));
	let distValues = $state<Record<string, number[]>>({});
	const finishedCells = $derived(run.cells.filter((c) => c.finished_at).length);
	$effect(() => {
		const id = run.id;
		const m = distMetric;
		void finishedCells;
		getRunDistribution(id, m)
			.then((d) => (distValues = d.cells))
			.catch(() => (distValues = {}));
	});
	const histGroups = $derived.by((): HistGroup[] => {
		const byCell = (c: ExplorerCell) => distValues[c.cell.id] ?? [];
		if (multiModel) {
			const ents = [...new Map(cells.map((c) => [c.entity, c])).values()];
			return ents.map((e) => ({
				name: e.entityLabel,
				color: seriesColor(e.entityIndex),
				values: cells.filter((c) => c.entity === e.entity).flatMap(byCell)
			}));
		}
		if (cells.length > 1 && cells.length <= 6) {
			return cells.map((c, i) => ({ name: cellDims(c), color: seriesColor(i), values: byCell(c) }));
		}
		return [{ name: 'All requests', color: seriesColor(0), values: cells.flatMap(byCell) }];
	});
	const distFmt = $derived(distMetrics.find((d) => d.key === distMetric)!.fmt);

	// Heatmap for runs varying two or more dimensions.
	let heatCols = $state<Dim>('load');
	let heatRows = $state<Dim>('context');
	let heatMetric = $state('ttft_p50');
	let heatFilters = $state<Partial<Record<Dim, string>>>({});
	const showHeat = $derived(varying.length >= 2);
	$effect(() => {
		if (!showHeat) return;
		if (!varying.includes(heatCols)) heatCols = varying.includes('load') ? 'load' : varying[0];
		if (!varying.includes(heatRows) || heatRows === heatCols) heatRows = varying.find((d) => d !== heatCols)!;
	});
	const heatOthers = $derived(varying.filter((d) => d !== heatCols && d !== heatRows));
	$effect(() => {
		for (const d of heatOthers) {
			const vals = dimValues(cells, d).map(String);
			if (!heatFilters[d] || !vals.includes(heatFilters[d]!)) heatFilters[d] = vals[0];
		}
	});
	function dimName(d: Dim): string {
		if (d === 'load') return loadKind === 'rate' ? 'Requests per second' : 'Concurrent users';
		return DIM_LABELS[d];
	}
	function valueLabel(d: Dim, v: string | number): string {
		switch (d) {
			case 'entity':
				return cells.find((c) => c.entity === v)?.entityLabel ?? String(v);
			case 'context':
				return `${formatTokens(Number(v))}`;
			case 'load':
				return loadKind === 'rate' ? `${v}/s` : String(v);
			default:
				return String(v);
		}
	}
	const heat = $derived.by(() => {
		const inFilter = cells.filter((c) => heatOthers.every((d) => String(dimValue(c, d)) === heatFilters[d]));
		const xs = dimValues(inFilter, heatCols);
		const ys = dimValues(inFilter, heatRows);
		const m = metricByKey(heatMetric);
		const values = ys.map((y) =>
			xs.map((x) => {
				const c = inFilter.find((c) => String(dimValue(c, heatCols)) === String(x) && String(dimValue(c, heatRows)) === String(y));
				return c ? m.get(c.summary) : null;
			})
		);
		return { xs, ys, values, m };
	});

	const selectClass = 'rounded-md border border-stone-300 bg-white px-2 py-1 text-xs';
</script>

<section class="space-y-4" aria-label="At a glance">
	<div class="grid grid-cols-2 gap-3 md:grid-cols-3 {slo ? 'xl:grid-cols-6' : 'xl:grid-cols-5'}">
		<StatTile label="Peak output throughput" value={peak ? rate(peak.summary.output_throughput) : '–'} sub={peak ? where(peak) : ''} />
		<StatTile label="Best time to first token (p50)" value={fastest ? ms(fastest.summary.ttft_ms.p50) : '–'} sub={fastest ? where(fastest) : ''} />
		<StatTile label="Fastest output per user" value={quickest ? rate(quickest.summary.output_tps.mean) : '–'} sub={quickest ? where(quickest) : ''} />
		<StatTile label="Success rate" value="{(100 * successRatio).toFixed(successRatio === 1 ? 0 : 1)}%" sub="{int(totals.ok)} ok · {int(totals.failed)} failed{totals.canceled ? ` · ${int(totals.canceled)} canceled` : ''}">
			<Meter ratio={successRatio} label="{(100 * successRatio).toFixed(1)}% of requests succeeded" />
		</StatTile>
		<StatTile label="Requests measured" value={int(totals.ok + totals.failed)} sub="{formatTokens(totals.input)} in · {formatTokens(totals.output)} out tokens" />
		{#if slo}
			{@const peakGood = bestGoodput ? `peak goodput ${bestGoodput.summary.goodput!.per_second.toFixed(2)} req/s · ${where(bestGoodput)}` : ''}
			{#if capacity}
				<StatTile
					label="Max load within SLO"
					value={capacity.within ? loadText(capacity.within) : 'none'}
					sub={capacity.within ? peakGood : `under ${Math.round(100 * SLO_ATTAINMENT)}% even at the lowest load`}
				>
					<Meter ratio={sloRatio ?? 0} goodAbove={SLO_ATTAINMENT} warnAbove={SLO_ATTAINMENT / 2} label="{Math.round(100 * (sloRatio ?? 0))}% of all requests within the SLO" />
				</StatTile>
			{:else}
				<StatTile label="Requests within SLO" value={sloRatio == null ? '–' : `${(100 * sloRatio).toFixed(sloRatio === 1 ? 0 : 1)}%`} sub={peakGood}>
					<Meter ratio={sloRatio ?? 0} goodAbove={SLO_ATTAINMENT} warnAbove={SLO_ATTAINMENT / 2} label="{Math.round(100 * (sloRatio ?? 0))}% of requests within the SLO" />
				</StatTile>
			{/if}
		{/if}
	</div>

	{#if notes.length}
		<div class="rounded-lg border border-stone-200 bg-white px-5 py-4">
			<h2 class="mb-2 text-sm font-semibold">What this run shows</h2>
			<ul class="space-y-1.5 text-sm text-stone-700">
				{#each notes as n, i (i)}
					<li class="flex gap-2">
						<span class="w-4 shrink-0 text-center {n.kind === 'warning' ? 'text-red-700' : n.kind === 'best' ? 'text-green-800' : 'text-blue-700'}" aria-hidden="true">
							{n.kind === 'warning' ? '⚠' : n.kind === 'best' ? '★' : '↗'}
						</span>
						<span>{n.text}</span>
					</li>
				{/each}
			</ul>
		</div>
	{/if}

	<div class="grid grid-cols-1 gap-4 lg:grid-cols-3">
		<div class="rounded-lg border border-stone-200 bg-white p-4">
			<h3 class="mb-2 text-sm font-medium">Request outcomes</h3>
			{#if outcomeSlices.filter((s) => s.value > 0).length > 1}
				<Donut slices={outcomeSlices} center="{(100 * successRatio).toFixed(0)}%" centerSub="succeeded" ariaLabel="Request outcomes" />
			{:else}
				<p class="py-6 text-center text-sm text-stone-600">
					<span class="text-green-800">✓</span> All {int(totals.ok)} measured requests succeeded.
				</p>
			{/if}
		</div>
		<div class="rounded-lg border border-stone-200 bg-white p-4">
			<h3 class="mb-2 text-sm font-medium">Where the tokens went</h3>
			<Donut slices={tokenSlices} center={formatTokens(totals.input + totals.output)} centerSub="tokens" format={(v) => int(v)} ariaLabel="Token composition" />
		</div>
		<div class="rounded-lg border border-stone-200 bg-white p-4">
			<div class="mb-1 flex items-center justify-between gap-2">
				<h3 class="text-sm font-medium">Distribution</h3>
				<select class={selectClass} bind:value={distMetric} aria-label="Distribution metric">
					{#each distMetrics as d (d.key)}<option value={d.key}>{d.label}</option>{/each}
				</select>
			</div>
			<p class="mb-1 text-[11px] text-stone-400">Share of successful requests per range{histGroups.length > 1 ? `, one line per ${multiModel ? 'model' : 'step'}` : ''}.</p>
			{#if histGroups.some((g) => g.values.length)}
				<Histogram groups={histGroups} format={distFmt} ariaLabel="Distribution of {distMetric}" />
			{:else}
				<p class="py-6 text-center text-sm text-stone-400">No data yet.</p>
			{/if}
		</div>
	</div>

	{#if showHeat}
		<div class="rounded-lg border border-stone-200 bg-white p-5">
			<div class="mb-3 flex flex-wrap items-center gap-3 text-xs">
				<h3 class="mr-2 text-sm font-medium">Heatmap</h3>
				<label class="flex items-center gap-1.5"><span class="text-stone-500">Metric</span>
					<select class={selectClass} bind:value={heatMetric}>{#each metricsFor(cells) as m (m.key)}<option value={m.key}>{m.label}</option>{/each}</select>
				</label>
				<label class="flex items-center gap-1.5"><span class="text-stone-500">Columns</span>
					<select class={selectClass} bind:value={heatCols}>{#each varying as d (d)}<option value={d}>{dimName(d)}</option>{/each}</select>
				</label>
				<label class="flex items-center gap-1.5"><span class="text-stone-500">Rows</span>
					<select class={selectClass} bind:value={heatRows}>{#each varying.filter((d) => d !== heatCols) as d (d)}<option value={d}>{dimName(d)}</option>{/each}</select>
				</label>
				{#each heatOthers as d (d)}
					<label class="flex items-center gap-1.5"><span class="text-stone-500">{dimName(d)}</span>
						<select class={selectClass} bind:value={heatFilters[d]}>
							{#each dimValues(cells, d) as v (v)}<option value={String(v)}>{valueLabel(d, v)}</option>{/each}
						</select>
					</label>
				{/each}
			</div>
			<Heatmap
				xLabels={heat.xs.map((x) => valueLabel(heatCols, x))}
				yLabels={heat.ys.map((y) => valueLabel(heatRows, y))}
				xTitle={dimName(heatCols)}
				yTitle={dimName(heatRows)}
				values={heat.values}
				format={(v) => heat.m.fmt(v)}
				lowerIsBetter={heat.m.lowerIsBetter}
			/>
		</div>
	{/if}
</section>
