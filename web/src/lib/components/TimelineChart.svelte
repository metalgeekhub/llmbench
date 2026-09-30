<script lang="ts">
	import { onMount } from 'svelte';
	import * as echarts from 'echarts/core';
	import { LineChart, type LineSeriesOption } from 'echarts/charts';
	import {
		GridComponent,
		LegendComponent,
		TooltipComponent,
		type GridComponentOption,
		type LegendComponentOption,
		type TooltipComponentOption
	} from 'echarts/components';
	import { SVGRenderer } from 'echarts/renderers';
	import type { Timeline } from '$lib/types';

	echarts.use([LineChart, GridComponent, LegendComponent, TooltipComponent, SVGRenderer]);

	type Option = echarts.ComposeOption<
		LineSeriesOption | GridComponentOption | LegendComponentOption | TooltipComponentOption
	>;

	let { timeline }: { timeline: Timeline } = $props();

	// Reference palette slots 1–2 (validated light mode) and text/grid tokens.
	const COLOR_ANSWER = '#2a78d6';
	const COLOR_REASONING = '#eb6834';
	const TEXT_SECONDARY = '#52514e';
	const GRID = '#e7e5e4';

	let el: HTMLDivElement;

	function buildOption(tl: Timeline): Option {
		const answer: [number, number][] = [];
		const reasoning: [number, number][] = [];
		tl.t.forEach((t, i) => {
			(tl.k[i] === 'r' ? reasoning : answer).push([t, i + 1]);
		});
		const both = answer.length > 0 && reasoning.length > 0;

		const series = (name: string, data: [number, number][], color: string): LineSeriesOption => ({
			name,
			type: 'line',
			data,
			color,
			showSymbol: data.length === 1,
			symbolSize: 8,
			lineStyle: { width: 2, cap: 'round', join: 'round' },
			emphasis: { disabled: true }
		});

		return {
			animation: false,
			grid: { left: 48, right: 16, top: both ? 32 : 12, bottom: 36 },
			// A legend only when there are two series; one series is named by the heading.
			legend: both
				? {
						top: 0,
						left: 0,
						icon: 'roundRect',
						itemWidth: 12,
						itemHeight: 4,
						textStyle: { color: TEXT_SECONDARY, fontSize: 11 }
					}
				: { show: false },
			tooltip: {
				trigger: 'axis',
				axisPointer: { type: 'line', lineStyle: { color: TEXT_SECONDARY, width: 1 } },
				textStyle: { fontSize: 12 },
				formatter: (params) => {
					const list = Array.isArray(params) ? params : [params];
					const p = list[0];
					if (!p) return '';
					const [t, n] = p.value as [number, number];
					return `<div style="font-size:12px"><b>${t.toFixed(1)} ms</b><br/>chunk #${n} · ${p.seriesName}</div>`;
				}
			},
			xAxis: {
				type: 'value',
				name: 'ms since request sent',
				nameLocation: 'middle',
				nameGap: 24,
				nameTextStyle: { color: TEXT_SECONDARY, fontSize: 11 },
				min: 0,
				axisLabel: { color: TEXT_SECONDARY, fontSize: 11 },
				axisLine: { lineStyle: { color: GRID } },
				splitLine: { lineStyle: { color: GRID, width: 1, type: 'solid' } }
			},
			yAxis: {
				type: 'value',
				minInterval: 1,
				axisLabel: { color: TEXT_SECONDARY, fontSize: 11 },
				splitLine: { lineStyle: { color: GRID, width: 1, type: 'solid' } }
			},
			series: [
				...(reasoning.length ? [series('Reasoning', reasoning, COLOR_REASONING)] : []),
				...(answer.length ? [series('Answer', answer, COLOR_ANSWER)] : [])
			]
		};
	}

	let chart = $state.raw<echarts.ECharts>();

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
		chart?.setOption(buildOption(timeline), true);
	});
</script>

<div bind:this={el} class="h-56 w-full" role="img" aria-label="Cumulative streamed chunks over time"></div>
