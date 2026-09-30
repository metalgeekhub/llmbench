// Chart options shared by the interactive charts and the static reports.

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
import { GRID, TEXT_SECONDARY } from './palette';

echarts.use([LineChart, BarChart, GridComponent, LegendComponent, TooltipComponent, SVGRenderer]);

export type DimChartOption = echarts.ComposeOption<
	LineSeriesOption | BarSeriesOption | GridComponentOption | LegendComponentOption | TooltipComponentOption
>;

export interface DimSeries {
	name: string;
	color: string;
	/** y per x value (numeric x) or per category (category x); null = no data. */
	data: [number | string, number | null][];
}

export interface DimChartInput {
	series: DimSeries[];
	xLabel: string;
	/** When set, the x axis is categorical (grouped bars) in this order. */
	categories?: string[];
	format?: (v: number) => string;
	xFormat?: (v: number | string) => string;
}

/** A metric along one dimension: lines over numeric x, grouped bars over categories. */
export function dimChartOption({
	series,
	xLabel,
	categories = [],
	format = (v) => String(v),
	xFormat = (v) => String(v)
}: DimChartInput): DimChartOption {
	const legend = series.length > 1;
	const categorical = categories.length > 0;
	const xs = series.flatMap((s) => s.data.map((d) => Number(d[0]))).filter((v) => v > 0);
	// Doubling steps (users, context lengths) read best on a log2 axis.
	const log2 = !categorical && xs.length > 0 && Math.max(...xs) / Math.min(...xs) >= 4;

	// Long series names wrap the legend; leave room for every row (~6.5px per
	// character at 11px, plus the icon and gap, in a ~500px wide chart).
	const legendWidth = series.reduce((w, s) => w + s.name.length * 6.5 + 32, 0);
	const legendRows = legend ? Math.min(4, Math.ceil(legendWidth / 500)) : 0;

	return {
		animation: false,
		grid: { left: 64, right: 20, top: legend ? 16 + 20 * legendRows : 16, bottom: 44 },
		legend: legend
			? { top: 0, left: 0, icon: 'roundRect', itemWidth: 12, itemHeight: 4, textStyle: { color: TEXT_SECONDARY, fontSize: 11 } }
			: { show: false },
		tooltip: {
			trigger: 'axis',
			axisPointer: categorical ? { type: 'shadow' } : { type: 'line', lineStyle: { color: TEXT_SECONDARY, width: 1 } },
			formatter: (params) => {
				const list = Array.isArray(params) ? params : [params];
				if (!list.length) return '';
				const first = list[0];
				const x = categorical ? first.name : (first.value as [number, number])[0];
				const rows = list
					.map((p) => {
						const v = categorical ? (p.value as number | null) : (p.value as [number, number | null])[1];
						return `<div style="display:flex;gap:8px;align-items:center">${p.marker}<span style="flex:1">${p.seriesName}</span><b>${v == null ? '–' : format(v)}</b></div>`;
					})
					.join('');
				return `<div style="font-size:12px;min-width:170px"><div style="margin-bottom:4px;color:${TEXT_SECONDARY}">${xFormat(x)}</div>${rows}</div>`;
			}
		},
		xAxis: categorical
			? {
					type: 'category',
					data: categories,
					name: xLabel,
					nameLocation: 'middle',
					nameGap: 28,
					nameTextStyle: { color: TEXT_SECONDARY, fontSize: 11 },
					axisLabel: { color: TEXT_SECONDARY, fontSize: 11, formatter: (v: string) => xFormat(v) },
					axisLine: { lineStyle: { color: GRID } },
					axisTick: { show: false }
				}
			: {
					type: log2 ? 'log' : 'value',
					logBase: 2,
					name: xLabel,
					nameLocation: 'middle',
					nameGap: 28,
					nameTextStyle: { color: TEXT_SECONDARY, fontSize: 11 },
					axisLabel: { color: TEXT_SECONDARY, fontSize: 11, formatter: (v: number) => xFormat(v) },
					axisLine: { lineStyle: { color: GRID } },
					splitLine: { lineStyle: { color: GRID, width: 1, type: 'solid' } }
				},
		yAxis: {
			type: 'value',
			min: 0,
			axisLabel: { color: TEXT_SECONDARY, fontSize: 11, formatter: (v: number) => format(v) },
			splitLine: { lineStyle: { color: GRID, width: 1, type: 'solid' } }
		},
		series: series.map((s) =>
			categorical
				? ({
						name: s.name,
						type: 'bar',
						color: s.color,
						data: categories.map((cat) => s.data.find((d) => String(d[0]) === cat)?.[1] ?? null),
						barMaxWidth: 24,
						barGap: '12%',
						itemStyle: { borderRadius: [4, 4, 0, 0] },
						emphasis: { disabled: true }
					} satisfies BarSeriesOption)
				: ({
						name: s.name,
						type: 'line',
						color: s.color,
						data: s.data.filter((d) => d[1] != null) as [number, number][],
						symbol: 'circle',
						symbolSize: 8,
						itemStyle: { borderColor: '#ffffff', borderWidth: 2 },
						lineStyle: { width: 2, cap: 'round', join: 'round' },
						emphasis: { disabled: true }
					} satisfies LineSeriesOption)
		)
	};
}

/** Renders a chart to a standalone SVG string (for reports). */
export function renderSVG(option: DimChartOption, width: number, height: number): string {
	const chart = echarts.init(null, null, { renderer: 'svg', ssr: true, width, height });
	try {
		chart.setOption({ ...option, tooltip: { show: false } });
		return chart.renderToSVGString();
	} finally {
		chart.dispose();
	}
}
