<script lang="ts">
	import { onMount } from 'svelte';
	import * as echarts from 'echarts/core';
	import { dimChartOption, type DimSeries } from '$lib/charts';

	let {
		series,
		xLabel,
		categories = [],
		format = (v: number) => String(v),
		xFormat = (v: number | string) => String(v),
		ariaLabel
	}: {
		series: DimSeries[];
		xLabel: string;
		/** When set, the x axis is categorical (grouped bars) in this order. */
		categories?: string[];
		format?: (v: number) => string;
		xFormat?: (v: number | string) => string;
		ariaLabel: string;
	} = $props();

	let el: HTMLDivElement;
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
		chart?.setOption(dimChartOption({ series, xLabel, categories, format, xFormat }), true);
	});
</script>

<div bind:this={el} class="h-72 w-full" role="img" aria-label={ariaLabel}></div>
