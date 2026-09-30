<script lang="ts">
	import { onMount } from 'svelte';
	import * as echarts from 'echarts/core';
	import { LineChart, type LineSeriesOption } from 'echarts/charts';
	import {
		GridComponent,
		LegendComponent,
		MarkLineComponent,
		TooltipComponent,
		type GridComponentOption,
		type LegendComponentOption,
		type MarkLineComponentOption,
		type TooltipComponentOption
	} from 'echarts/components';
	import { SVGRenderer } from 'echarts/renderers';
	import { GRID, TEXT_SECONDARY } from '$lib/palette';

	echarts.use([LineChart, GridComponent, LegendComponent, MarkLineComponent, TooltipComponent, SVGRenderer]);

	type Option = echarts.ComposeOption<
		| LineSeriesOption
		| GridComponentOption
		| LegendComponentOption
		| TooltipComponentOption
		| MarkLineComponentOption
	>;

	export interface TimeSeries {
		name: string;
		color: string;
		/** [seconds since start, value]; null values leave a gap. */
		data: [number, number | null][];
	}

	export interface Marker {
		t: number;
		label: string;
	}

	let {
		series,
		markers = [],
		format = (v: number) => String(Math.round(v)),
		step = false,
		ariaLabel
	}: {
		series: TimeSeries[];
		/** Vertical annotations, e.g. where each benchmark step starts. */
		markers?: Marker[];
		format?: (v: number) => string;
		/** Draw as a step function (counts such as active users). */
		step?: boolean;
		ariaLabel: string;
	} = $props();

	let el: HTMLDivElement;
	let chart = $state.raw<echarts.ECharts>();

	function fmtTime(s: number): string {
		if (s < 60) return `${Math.round(s)}s`;
		return `${Math.floor(s / 60)}m${String(Math.round(s % 60)).padStart(2, '0')}`;
	}

	function buildOption(): Option {
		const legend = series.length > 1;
		return {
			animation: false,
			grid: { left: 56, right: 16, top: legend || markers.length ? 34 : 14, bottom: 28 },
			legend: legend
				? { top: 0, left: 0, icon: 'roundRect', itemWidth: 12, itemHeight: 4, textStyle: { color: TEXT_SECONDARY, fontSize: 11 } }
				: { show: false },
			tooltip: {
				trigger: 'axis',
				axisPointer: { type: 'line', lineStyle: { color: TEXT_SECONDARY, width: 1 } },
				formatter: (params) => {
					const list = Array.isArray(params) ? params : [params];
					if (!list.length) return '';
					const t = (list[0].value as [number, number])[0];
					const rows = list
						.map((p) => {
							const v = (p.value as [number, number | null])[1];
							return `<div style="display:flex;gap:8px;align-items:center">${p.marker}<span style="flex:1">${p.seriesName}</span><b>${v == null ? '–' : format(v)}</b></div>`;
						})
						.join('');
					return `<div style="font-size:12px;min-width:150px"><div style="margin-bottom:4px;color:${TEXT_SECONDARY}">${fmtTime(t)}</div>${rows}</div>`;
				}
			},
			xAxis: {
				type: 'value',
				min: 0,
				axisLabel: { color: TEXT_SECONDARY, fontSize: 11, formatter: (v: number) => fmtTime(v) },
				axisLine: { lineStyle: { color: GRID } },
				splitLine: { show: false }
			},
			yAxis: {
				type: 'value',
				min: 0,
				axisLabel: { color: TEXT_SECONDARY, fontSize: 11, formatter: (v: number) => format(v) },
				splitLine: { lineStyle: { color: GRID, width: 1, type: 'solid' } }
			},
			series: series.map((s, i) => ({
				name: s.name,
				type: 'line',
				data: s.data,
				color: s.color,
				step: step ? 'end' : false,
				showSymbol: false,
				connectNulls: false,
				sampling: 'lttb',
				lineStyle: { width: 2, cap: 'round', join: 'round' },
				emphasis: { disabled: true },
				// Step boundaries, drawn once (on the first series).
				markLine:
					i === 0 && markers.length
						? {
								silent: true,
								symbol: 'none',
								lineStyle: { color: GRID, width: 1, type: 'solid' },
								label: { formatter: '{b}', position: 'end', color: TEXT_SECONDARY, fontSize: 10 },
								data: markers.map((m) => ({ xAxis: m.t, name: m.label }))
							}
						: undefined
			}))
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

<div bind:this={el} class="h-48 w-full" role="img" aria-label={ariaLabel}></div>
