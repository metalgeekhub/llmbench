<script lang="ts">
	import { onMount } from 'svelte';
	import * as echarts from 'echarts/core';
	import { BarChart, LineChart, type BarSeriesOption, type LineSeriesOption } from 'echarts/charts';
	import {
		GridComponent,
		LegendComponent,
		TooltipComponent,
		type GridComponentOption,
		type LegendComponentOption,
		type TooltipComponentOption
	} from 'echarts/components';
	import { SVGRenderer } from 'echarts/renderers';
	import { GRID, TEXT_SECONDARY } from '$lib/palette';

	echarts.use([BarChart, LineChart, GridComponent, LegendComponent, TooltipComponent, SVGRenderer]);

	type Option = echarts.ComposeOption<
		BarSeriesOption | LineSeriesOption | GridComponentOption | LegendComponentOption | TooltipComponentOption
	>;

	export interface HistGroup {
		name: string;
		color: string;
		values: number[];
	}

	let {
		groups,
		format = (v: number) => String(Math.round(v)),
		bins = 24,
		ariaLabel
	}: {
		groups: HistGroup[];
		format?: (v: number) => string;
		bins?: number;
		ariaLabel: string;
	} = $props();

	let el: HTMLDivElement;
	let chart = $state.raw<echarts.ECharts>();

	// Shared bin edges from the pooled values; the top 1% is folded into the
	// last bin so one outlier doesn't flatten the chart.
	const layout = $derived.by(() => {
		const all = groups.flatMap((g) => g.values).sort((a, b) => a - b);
		if (all.length === 0) return { edges: [] as number[], counts: [] as number[][], capped: false };
		const lo = all[0];
		const p99 = all[Math.min(all.length - 1, Math.floor(all.length * 0.99))];
		const hi = p99 > lo ? p99 : lo + 1;
		const width = (hi - lo) / bins;
		const edges = Array.from({ length: bins + 1 }, (_, i) => lo + i * width);
		const counts = groups.map((g) => {
			const c = new Array(bins).fill(0);
			for (const v of g.values) c[Math.min(bins - 1, Math.max(0, Math.floor((v - lo) / width)))]++;
			// Share of the group's requests, so groups of different sizes compare.
			return c.map((n) => (g.values.length ? (100 * n) / g.values.length : 0));
		});
		return { edges, counts, capped: all[all.length - 1] > hi };
	});

	function buildOption(): Option {
		const { edges, counts } = layout;
		const labels = edges.slice(0, -1).map((e, i) => `${format(e)}–${format(edges[i + 1])}`);
		const single = groups.length === 1;
		return {
			animation: false,
			grid: { left: 44, right: 16, top: single ? 12 : groups.length > 2 ? 54 : 32, bottom: 36 },
			legend: single
				? { show: false }
				: { top: 0, left: 0, icon: 'roundRect', itemWidth: 12, itemHeight: 4, textStyle: { color: TEXT_SECONDARY, fontSize: 11 } },
			tooltip: {
				trigger: 'axis',
				axisPointer: { type: 'shadow' },
				formatter: (params) => {
					const list = Array.isArray(params) ? params : [params];
					if (!list.length) return '';
					const rows = list
						.map((p) => `<div style="display:flex;gap:8px;align-items:center">${p.marker}<span style="flex:1">${p.seriesName}</span><b>${(p.value as number).toFixed(1)}%</b></div>`)
						.join('');
					return `<div style="font-size:12px;min-width:160px"><div style="margin-bottom:4px;color:${TEXT_SECONDARY}">${list[0].name}</div>${rows}</div>`;
				}
			},
			xAxis: {
				type: 'category',
				data: labels,
				axisLabel: { color: TEXT_SECONDARY, fontSize: 10, interval: Math.ceil(labels.length / 6) - 1, formatter: (v: string) => v.split('–')[0] },
				axisLine: { lineStyle: { color: GRID } },
				axisTick: { show: false }
			},
			yAxis: {
				type: 'value',
				axisLabel: { color: TEXT_SECONDARY, fontSize: 10, formatter: (v: number) => `${v}%` },
				splitLine: { lineStyle: { color: GRID, width: 1, type: 'solid' } }
			},
			series: groups.map((g, i) =>
				single
					? ({ name: g.name, type: 'bar', data: counts[i], color: g.color, barCategoryGap: '8%', itemStyle: { borderRadius: [3, 3, 0, 0] }, emphasis: { disabled: true } } satisfies BarSeriesOption)
					: ({ name: g.name, type: 'line', data: counts[i], color: g.color, step: 'middle', showSymbol: false, lineStyle: { width: 2 }, emphasis: { disabled: true } } satisfies LineSeriesOption)
			)
		};
	}

	onMount(() => {
		const c = echarts.init(el, undefined, { renderer: 'svg' });
		const ro = new ResizeObserver(() => c.resize());
		ro.observe(el);
		chart = c;
		return () => {
			ro.disconnect();
			c.dispose();
		};
	});

	$effect(() => {
		chart?.setOption(buildOption(), true);
	});
</script>

<div bind:this={el} class="h-44 w-full" role="img" aria-label={ariaLabel}></div>
{#if layout.capped}
	<p class="text-[11px] text-stone-400">The slowest 1% are counted in the last bar.</p>
{/if}
