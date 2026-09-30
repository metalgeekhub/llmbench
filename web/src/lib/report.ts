// Shareable reports (HTML with inline SVG charts, or Markdown), generated in
// the browser from the same data and findings the pages show.

import { dimChartOption, renderSVG, type DimSeries } from './charts';
import {
	cellTargetLabel,
	DIM_LABELS,
	dimValue,
	dimValueLabel,
	dimValues,
	formatTokens,
	limitSummary,
	loadLabel,
	metricByKey,
	promptSummary,
	sloSummary,
	typeLabel,
	type Dim,
	type ExplorerCell
} from './dims';
import { dateTime, duration, int, ms, pct, rate } from './format';
import { errorFinding, findings, goodputFindings, winnerTiles, type Finding } from './insights';
import { seriesColor } from './palette';
import type { Aggregate, Goodput, Run, RunCell, Summary } from './types';

export type ReportFormat = 'html' | 'md';

interface Tile {
	label: string;
	value: string;
	sub: string;
}

interface Table {
	title: string;
	note?: string;
	head: string[];
	/** 'l' or 'r' per column. */
	align: string;
	rows: string[][];
}

interface Chart {
	title: string;
	svg: string;
}

interface ReportDoc {
	title: string;
	meta: string[];
	tiles: Tile[];
	notes: Finding[];
	charts: Chart[];
	tables: Table[];
}

const ALL_DIMS: Dim[] = ['load', 'context', 'thinking', 'entity'];
const MAX_CHART_SERIES = 8;
const DASH = '–';

// --- charts ---

/** One chart per metric: the first swept dimension on x, the others as lines. */
function chartsFor(cells: ExplorerCell[], metricKeys: string[], entityNoun: string): Chart[] {
	if (cells.length < 2) return [];
	const varying = ALL_DIMS.filter((d) => dimValues(cells, d).length > 1);
	if (!varying.length) return [];
	const x = (['load', 'context', 'thinking'] as Dim[]).find((d) => varying.includes(d)) ?? 'entity';
	const seriesDims = varying.filter((d) => d !== x);
	const loadKind = cells[0].loadKind;
	const groups = new Map<string, ExplorerCell[]>();
	for (const c of cells) {
		const key = seriesDims.map((d) => String(dimValue(c, d))).join('|');
		groups.set(key, [...(groups.get(key) ?? []), c]);
	}
	const entries = [...groups.values()].slice(0, MAX_CHART_SERIES);
	const onlyEntity = seriesDims.length === 1 && seriesDims[0] === 'entity';
	const xVals = dimValues(cells, x);
	const categorical = x === 'thinking' || x === 'entity';
	const xName = x === 'load' ? (loadKind === 'rate' ? 'Requests per second' : 'Concurrent users') : x === 'entity' ? entityNoun : DIM_LABELS[x];
	const xFormat = (v: number | string) => {
		if (x === 'context') return formatTokens(Number(v));
		if (x === 'entity') return cells.find((c) => c.entity === v)?.entityLabel ?? String(v);
		return String(v);
	};

	return metricKeys.map((key) => {
		const m = metricByKey(key);
		const series: DimSeries[] = entries.map((members, i) => ({
			name:
				seriesDims
					.map((d) => (d === 'entity' ? members[0].entityLabel : dimValueLabel(d, dimValue(members[0], d), loadKind)))
					.join(' · ') || m.label,
			color: onlyEntity ? seriesColor(members[0].entityIndex) : seriesColor(i),
			data: xVals.map((xv) => {
				const c = members.find((c) => String(dimValue(c, x)) === String(xv));
				return [categorical ? String(xv) : Number(xv), c ? m.get(c.summary) : null];
			})
		}));
		const option = dimChartOption({
			series,
			xLabel: xName,
			categories: categorical ? xVals.map(String) : [],
			format: (v) => m.fmt(v),
			xFormat
		});
		return { title: `${m.label} by ${xName.toLowerCase()}`, svg: renderSVG(option, 540, 300) };
	});
}

// --- shared pieces ---

function bestBy(cells: ExplorerCell[], get: (s: Aggregate) => number | null, lower: boolean): ExplorerCell | null {
	let best: ExplorerCell | null = null;
	let bv = 0;
	for (const c of cells) {
		const v = get(c.summary);
		if (v == null) continue;
		if (!best || (lower ? v < bv : v > bv)) {
			best = c;
			bv = v;
		}
	}
	return best;
}

function goodputCols(g: Goodput | undefined): string[] {
	if (!g || !g.requests) return [DASH, DASH];
	return [pct(100 * g.ratio, 0), g.per_second.toFixed(2)];
}

const p = (s: Summary, k: 'p50' | 'p95', fmt: (v: number) => string) => (s.count ? fmt(s[k]) : DASH);

function percentileTable(rows: { label: string; summary: Aggregate }[]): Table {
	const metrics: [string, keyof Aggregate][] = [
		['TTFT', 'ttft_ms'],
		['TPOT', 'tpot_ms'],
		['E2E', 'e2e_ms']
	];
	const cols = ['min', 'mean', 'p50', 'p90', 'p95', 'p99', 'max'] as const;
	return {
		title: 'Latency percentiles',
		note: 'Successful requests only; TPOT excludes single-token answers.',
		head: ['Step', 'Metric', ...cols],
		align: 'll' + 'r'.repeat(cols.length),
		rows: rows.flatMap((r) =>
			metrics.flatMap(([name, key]) => {
				const s = r.summary[key] as Summary;
				return s.count ? [[r.label, name, ...cols.map((c) => ms(s[c]))]] : [];
			})
		)
	};
}

function sentence(f: Finding): string {
	return `${f.kind === 'warning' ? '⚠' : f.kind === 'best' ? '★' : '↗'} ${f.text}`;
}

// --- run report ---

export interface RunReportInput {
	run: Run;
	/** Measured cells, with goodput attached when an SLO is set. */
	cells: ExplorerCell[];
	goodput: Record<string, Goodput> | null;
	/** The SLO the goodput was evaluated against. */
	slo: Run['config']['slo'];
}

const RUN_METRICS: Record<string, string[]> = {
	thinking_comparison: ['ttfat_p50', 'e2e_p50'],
	context_sweep: ['ttft_p50', 'user_tps'],
	default: ['output_throughput', 'ttft_p95']
};

function runDoc({ run, cells, goodput, slo }: RunReportInput, withCharts: boolean): ReportDoc {
	const summaries = run.cells.flatMap((c) => (c.summary ? [c.summary] : []));
	const ok = summaries.reduce((n, s) => n + s.succeeded, 0);
	const failed = summaries.reduce((n, s) => n + s.failed, 0);
	const multiModel = run.config.targets.length > 1;
	const sweepsContext = !!run.config.context_lengths?.length;
	const sweepsThinking = !!run.config.thinking_levels?.length;
	const openLoop = run.config.load_model === 'open';
	const dimsOf = (c: RunCell) => {
		const parts = [];
		if (sweepsContext) parts.push(`${formatTokens(c.context_tokens)} tokens`);
		if (sweepsThinking) parts.push(`thinking ${c.thinking}`);
		parts.push(loadLabel(c));
		return parts.join(' · ');
	};
	const where = (c: ExplorerCell) => (multiModel ? `${c.entityLabel} · ${dimsOf(c.cell)}` : dimsOf(c.cell));

	const meta = [`${typeLabel(run)} · ${run.status} · started ${dateTime(run.created_at)}`];
	if (run.started_at && run.finished_at) {
		meta[0] += ` · took ${duration((new Date(run.finished_at).getTime() - new Date(run.started_at).getTime()) / 1000)}`;
	}
	meta.push(`Models: ${[...new Set(run.cells.map((c) => cellTargetLabel(run, c)))].join(', ')}`);
	meta.push(promptSummary(run), limitSummary(run));
	if (slo) meta.push(`SLO: ${sloSummary(slo)}`);
	if (run.imported_at) meta.push(`Imported ${dateTime(run.imported_at)}`);

	const peak = bestBy(cells, (s) => s.output_throughput, false);
	const fastest = bestBy(cells, (s) => (s.ttft_ms.count ? s.ttft_ms.p50 : null), true);
	const quickest = bestBy(cells, (s) => (s.output_tps.count ? s.output_tps.mean : null), false);
	const tiles: Tile[] = [
		{ label: 'Peak output throughput', value: peak ? rate(peak.summary.output_throughput) : DASH, sub: peak ? where(peak) : '' },
		{ label: 'Best TTFT (p50)', value: fastest ? ms(fastest.summary.ttft_ms.p50) : DASH, sub: fastest ? where(fastest) : '' },
		{ label: 'Fastest output per user', value: quickest ? rate(quickest.summary.output_tps.mean) : DASH, sub: quickest ? where(quickest) : '' },
		{ label: 'Success rate', value: ok + failed ? pct((100 * ok) / (ok + failed)) : DASH, sub: `${int(ok)} ok · ${int(failed)} failed` }
	];
	if (slo) {
		const gs = cells.flatMap((c) => (c.summary.goodput?.requests ? [c.summary.goodput] : []));
		const good = gs.reduce((n, g) => n + g.good, 0);
		const total = gs.reduce((n, g) => n + g.requests, 0);
		const best = bestBy(cells, (s) => s.goodput?.per_second ?? null, false);
		tiles.push({
			label: 'Requests within SLO',
			value: total ? pct((100 * good) / total) : DASH,
			sub: best ? `peak goodput ${best.summary.goodput!.per_second.toFixed(2)} req/s · ${where(best)}` : ''
		});
	}

	const varying = ALL_DIMS.filter((d) => dimValues(cells, d).length > 1);
	const err = errorFinding(summaries);
	const notes = [...(err ? [err] : []), ...goodputFindings(cells, varying), ...findings(cells, varying)];

	const metricKeys = [...(RUN_METRICS[run.type] ?? RUN_METRICS.default)];
	if (slo && goodput) metricKeys.push('goodput_rps');
	const charts = withCharts ? chartsFor(cells, metricKeys, 'Model') : [];

	const head = ['Model'];
	let align = 'l';
	if (sweepsContext) {
		head.push('Context');
		align += 'r';
	}
	if (sweepsThinking) {
		head.push('Thinking');
		align += 'l';
	}
	head.push(openLoop ? 'Rate' : 'Users', 'Status', 'Requests', 'Errors', 'Output tok/s', 'Req/s', 'TTFT p50', 'TTFT p95', 'E2E p50', 'E2E p95', 'TPOT p50', 'tok/s per user');
	align += 'rlrrrrrrrrrr';
	if (slo) {
		head.push('Within SLO', 'Goodput req/s');
		align += 'rr';
	}
	const rows = run.cells.map((c) => {
		const s = c.summary;
		const row = [cellTargetLabel(run, c)];
		if (sweepsContext) row.push(formatTokens(c.context_tokens));
		if (sweepsThinking) row.push(c.thinking);
		row.push(
			openLoop ? `${c.arrival_rate}/s` : String(c.concurrency),
			c.status,
			s ? int(s.requests) : DASH,
			s ? `${int(s.failed)} (${pct(100 * s.error_rate, 0)})` : DASH,
			s ? rate(s.output_throughput, '').trim() : DASH,
			s ? s.request_throughput.toFixed(2) : DASH,
			s ? p(s.ttft_ms, 'p50', ms) : DASH,
			s ? p(s.ttft_ms, 'p95', ms) : DASH,
			s ? p(s.e2e_ms, 'p50', ms) : DASH,
			s ? p(s.e2e_ms, 'p95', ms) : DASH,
			s ? p(s.tpot_ms, 'p50', ms) : DASH,
			s?.output_tps.count ? rate(s.output_tps.mean, '').trim() : DASH
		);
		if (slo) row.push(...goodputCols(goodput?.[c.id]));
		return row;
	});

	const label = (c: RunCell) => (multiModel ? `${cellTargetLabel(run, c)} · ${dimsOf(c)}` : dimsOf(c));
	return {
		title: run.name,
		meta,
		tiles,
		notes,
		charts,
		tables: [
			{ title: 'Results', note: 'Latencies from successful requests only.', head, align, rows },
			percentileTable(run.cells.flatMap((c) => (c.summary ? [{ label: label(c), summary: c.summary }] : [])))
		]
	};
}

export function runReport(format: ReportFormat, input: RunReportInput): string {
	const doc = runDoc(input, format === 'html');
	return format === 'html' ? renderHTML(doc) : renderMarkdown(doc);
}

// --- comparison report ---

export interface ComparisonReportInput {
	title: string;
	runs: Run[];
	/** Explorer cells of all runs (entity = run · model), goodput attached. */
	cells: ExplorerCell[];
	settings?: { metrics?: [string, string] };
}

function comparisonDoc({ title, runs, cells, settings }: ComparisonReportInput, withCharts: boolean): ReportDoc {
	const meta = runs.map((r) => {
		let line = `${r.name}: ${typeLabel(r)} · ${[...new Set(r.cells.map((c) => cellTargetLabel(r, c)))].join(', ')} · ${dateTime(r.created_at)}`;
		if (r.config.slo) line += ` · SLO ${sloSummary(r.config.slo)}`;
		return line;
	});
	const tiles = winnerTiles(cells).map((w) => ({
		label: w.label,
		value: w.value,
		sub: w.who ? `★ ${w.who}${w.margin ? ` · ${w.margin}` : ''}` : `= Tie · ${w.margin}`
	}));
	const varying = ALL_DIMS.filter((d) => dimValues(cells, d).length > 1);
	const notes = [...goodputFindings(cells, varying), ...findings(cells, varying)];

	const metricKeys = [...(settings?.metrics ?? ['output_throughput', 'ttft_p95'])];
	const hasGoodput = cells.some((c) => c.summary.goodput);
	if (hasGoodput && !metricKeys.includes('goodput_rps')) metricKeys.push('goodput_rps');
	const charts = withCharts ? chartsFor(cells, metricKeys, 'Run · model') : [];

	const dims = varying.filter((d) => d !== 'entity');
	const loadKind = cells[0]?.loadKind ?? 'users';
	const head = ['Run · model', ...dims.map((d) => (d === 'load' ? (loadKind === 'rate' ? 'Rate' : 'Users') : DIM_LABELS[d])), 'Requests', 'Error rate', 'Output tok/s', 'TTFT p50', 'TTFT p95', 'E2E p50', 'TPOT p50', 'tok/s per user'];
	let align = 'l' + 'l'.repeat(dims.length) + 'rrrrrrrr';
	if (hasGoodput) {
		head.push('Within SLO', 'Goodput req/s');
		align += 'rr';
	}
	const rows = cells.map((c) => {
		const s = c.summary;
		const row = [
			c.entityLabel,
			...dims.map((d) => dimValueLabel(d, dimValue(c, d), loadKind)),
			int(s.requests),
			pct(100 * s.error_rate),
			rate(s.output_throughput, '').trim(),
			p(s.ttft_ms, 'p50', ms),
			p(s.ttft_ms, 'p95', ms),
			p(s.e2e_ms, 'p50', ms),
			p(s.tpot_ms, 'p50', ms),
			s.output_tps.count ? rate(s.output_tps.mean, '').trim() : DASH
		];
		if (hasGoodput) row.push(...goodputCols(s.goodput));
		return row;
	});
	const label = (c: ExplorerCell) => [c.entityLabel, ...dims.map((d) => dimValueLabel(d, dimValue(c, d), loadKind))].join(' · ');
	return {
		title,
		meta,
		tiles,
		notes,
		charts,
		tables: [
			{ title: 'Results', note: 'One row per model and step. Latencies from successful requests only.', head, align, rows },
			percentileTable(cells.map((c) => ({ label: label(c), summary: c.summary })))
		]
	};
}

export function comparisonReport(format: ReportFormat, input: ComparisonReportInput): string {
	const doc = comparisonDoc(input, format === 'html');
	return format === 'html' ? renderHTML(doc) : renderMarkdown(doc);
}

// --- rendering ---

function esc(s: string): string {
	return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

const CSS = `
body{font:14px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;color:#1c1917;background:#fafaf9;margin:0}
main{max-width:1180px;margin:0 auto;padding:32px 24px}
h1{font-size:24px;margin:0 0 4px}h2{font-size:16px;margin:32px 0 8px}
.meta{color:#57534e;font-size:13px;margin:0}
.tiles{display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:12px;margin-top:24px}
.tile{background:#fff;border:1px solid #e7e5e4;border-radius:8px;padding:12px 16px}
.tile .l{font-size:12px;color:#78716c}.tile .v{font-size:22px;font-weight:600}.tile .s{font-size:12px;color:#78716c}
ul.notes{background:#fff;border:1px solid #e7e5e4;border-radius:8px;padding:12px 16px 12px 20px;margin:0;list-style:none}
ul.notes li{margin:4px 0}
.charts{display:grid;grid-template-columns:repeat(auto-fill,minmax(540px,1fr));gap:16px}
figure{background:#fff;border:1px solid #e7e5e4;border-radius:8px;margin:0;padding:12px}
figcaption{font-size:13px;font-weight:600;margin-bottom:4px}
figure svg{max-width:100%;height:auto}
.table{overflow-x:auto;background:#fff;border:1px solid #e7e5e4;border-radius:8px}
table{border-collapse:collapse;width:100%;font-size:12px}
th{background:#f5f5f4;color:#57534e;font-weight:500;white-space:nowrap}
th,td{padding:6px 10px;border-bottom:1px solid #f5f5f4}
td.r{text-align:right;font-family:ui-monospace,Consolas,monospace;white-space:nowrap}
.note{color:#78716c;font-size:12px;margin:-4px 0 8px}
footer{color:#a8a29e;font-size:12px;margin-top:40px}
@media print{body{background:#fff}.charts{grid-template-columns:1fr}}
`;

function renderHTML(doc: ReportDoc): string {
	const parts: string[] = [];
	parts.push(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">`);
	parts.push(`<title>${esc(doc.title)} · LLMBench report</title><style>${CSS}</style></head><body><main>`);
	parts.push(`<h1>${esc(doc.title)}</h1>`);
	for (const m of doc.meta) parts.push(`<p class="meta">${esc(m)}</p>`);
	if (doc.tiles.length) {
		parts.push('<div class="tiles">');
		for (const t of doc.tiles) {
			parts.push(`<div class="tile"><div class="l">${esc(t.label)}</div><div class="v">${esc(t.value)}</div><div class="s">${esc(t.sub)}</div></div>`);
		}
		parts.push('</div>');
	}
	if (doc.notes.length) {
		parts.push('<h2>What this shows</h2><ul class="notes">');
		for (const n of doc.notes) parts.push(`<li>${esc(sentence(n))}</li>`);
		parts.push('</ul>');
	}
	if (doc.charts.length) {
		parts.push('<h2>Charts</h2><div class="charts">');
		for (const c of doc.charts) parts.push(`<figure><figcaption>${esc(c.title)}</figcaption>${c.svg}</figure>`);
		parts.push('</div>');
	}
	for (const t of doc.tables) {
		if (!t.rows.length) continue;
		parts.push(`<h2>${esc(t.title)}</h2>`);
		if (t.note) parts.push(`<p class="note">${esc(t.note)}</p>`);
		parts.push('<div class="table"><table><thead><tr>');
		t.head.forEach((h, i) => parts.push(`<th style="text-align:${t.align[i] === 'r' ? 'right' : 'left'}">${esc(h)}</th>`));
		parts.push('</tr></thead><tbody>');
		for (const row of t.rows) {
			parts.push('<tr>');
			row.forEach((v, i) => parts.push(`<td${t.align[i] === 'r' ? ' class="r"' : ''}>${esc(v)}</td>`));
			parts.push('</tr>');
		}
		parts.push('</tbody></table></div>');
	}
	parts.push(`<footer>Generated by LLMBench on ${esc(dateTime(new Date().toISOString()))}.</footer></main></body></html>\n`);
	return parts.join('');
}

function mdCell(s: string): string {
	return s.replace(/\|/g, '\\|').replace(/\n/g, ' ');
}

function renderMarkdown(doc: ReportDoc): string {
	const out: string[] = [`# ${doc.title}`, ''];
	for (const m of doc.meta) out.push(`- ${m}`);
	out.push('');
	if (doc.tiles.length) {
		out.push('| ' + doc.tiles.map((t) => mdCell(t.label)).join(' | ') + ' |');
		out.push('|' + doc.tiles.map(() => ' --- ').join('|') + '|');
		out.push('| ' + doc.tiles.map((t) => `**${mdCell(t.value)}**${t.sub ? `<br>${mdCell(t.sub)}` : ''}`).join(' | ') + ' |');
		out.push('');
	}
	if (doc.notes.length) {
		out.push('## What this shows', '');
		for (const n of doc.notes) out.push(`- ${sentence(n)}`);
		out.push('');
	}
	for (const t of doc.tables) {
		if (!t.rows.length) continue;
		out.push(`## ${t.title}`, '');
		if (t.note) out.push(`_${t.note}_`, '');
		out.push('| ' + t.head.map(mdCell).join(' | ') + ' |');
		out.push('|' + t.head.map((_, i) => (t.align[i] === 'r' ? ' ---: ' : ' --- ')).join('|') + '|');
		for (const row of t.rows) out.push('| ' + row.map(mdCell).join(' | ') + ' |');
		out.push('');
	}
	out.push(`_Generated by LLMBench on ${dateTime(new Date().toISOString())}._`, '');
	return out.join('\n');
}
