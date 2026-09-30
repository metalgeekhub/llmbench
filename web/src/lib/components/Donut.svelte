<script lang="ts">
	import { onMount } from 'svelte';
	import * as echarts from 'echarts/core';
	import { PieChart, type PieSeriesOption } from 'echarts/charts';
	import { TooltipComponent, type TooltipComponentOption } from 'echarts/components';
	import { SVGRenderer } from 'echarts/renderers';
	import { TEXT_SECONDARY } from '$lib/palette';

	echarts.use([PieChart, TooltipComponent, SVGRenderer]);

	type Option = echarts.ComposeOption<PieSeriesOption | TooltipComponentOption>;

	export interface Slice {
		name: string;
		value: number;
		color: string;
	}

	let {
		slices,
		center = '',
		centerSub = '',
		format = (v: number) => v.toLocaleString(),
		ariaLabel
	}: {
		/** At most 6 slices read at a glance; callers fold the rest into "Other". */
		slices: Slice[];
		center?: string;
		centerSub?: string;
		format?: (v: number) => string;
		ariaLabel: string;
	} = $props();

	let el: HTMLDivElement;
	let chart = $state.raw<echarts.ECharts>();
	const total = $derived(slices.reduce((n, s) => n + s.value, 0));

	function buildOption(): Option {
		return {
			animation: false,
			tooltip: {
				trigger: 'item',
				formatter: (p) => {
					const d = Array.isArray(p) ? p[0] : p;
					const pct = total ? ((100 * (d.value as number)) / total).toFixed(1) : '0';
					return `<div style="font-size:12px">${d.marker}${d.name}<br/><b>${format(d.value as number)}</b> (${pct}%)</div>`;
				}
			},
			series: [
				{
					type: 'pie',
					radius: ['58%', '82%'],
					padAngle: 1.5,
					itemStyle: { borderColor: '#ffffff', borderWidth: 2, borderRadius: 3 },
					label: { show: false },
					emphasis: { scale: false },
					data: slices.filter((s) => s.value > 0).map((s) => ({ name: s.name, value: s.value, itemStyle: { color: s.color } }))
				}
			]
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

<div class="flex items-center gap-4">
	<div class="relative h-36 w-36 shrink-0">
		<div bind:this={el} class="h-full w-full" role="img" aria-label={ariaLabel}></div>
		{#if center}
			<div class="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
				<span class="text-lg font-semibold text-stone-900">{center}</span>
				{#if centerSub}<span class="text-[11px]" style="color: {TEXT_SECONDARY}">{centerSub}</span>{/if}
			</div>
		{/if}
	</div>
	<!-- Legend doubles as the table view: every slice with value and share. -->
	<ul class="min-w-0 flex-1 space-y-1 text-xs">
		{#each slices.filter((s) => s.value > 0) as s (s.name)}
			<li class="flex items-center gap-2">
				<span class="h-2.5 w-2.5 shrink-0 rounded-sm" style="background: {s.color}" aria-hidden="true"></span>
				<span class="min-w-0 flex-1 truncate text-stone-700" title={s.name}>{s.name}</span>
				<span class="font-mono text-stone-900">{format(s.value)}</span>
				<span class="w-12 text-right font-mono text-stone-500">{total ? ((100 * s.value) / total).toFixed(1) : '0'}%</span>
			</li>
		{/each}
	</ul>
</div>
