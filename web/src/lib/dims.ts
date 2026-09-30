// Benchmark matrix dimensions and chartable metrics, shared by the run page
// and the run comparison view.

import { duration, int, ms, pct, rate } from './format';
import type { Aggregate, Goodput, Run, RunCell, SLO } from './types';

export type Dim = 'entity' | 'load' | 'context' | 'thinking';

export const THINKING_ORDER = ['off', 'low', 'medium', 'high'];

/** One measured cell, normalized for charting. */
export interface ExplorerCell {
	/** Series key when grouping by entity (target on the run page, run · target in comparisons). */
	entity: string;
	entityLabel: string;
	entityIndex: number;
	load: number;
	loadKind: 'users' | 'rate';
	context: number; // 0 = not swept
	thinking: string; // '' = not swept
	summary: Aggregate;
	cell: RunCell;
}

export function formatTokens(n: number): string {
	if (n >= 1024 && n % 1024 === 0) return `${n / 1024}k`;
	if (n >= 1000) return `${(n / 1000).toFixed(n % 1000 === 0 ? 0 : 1)}k`;
	return String(n);
}

export function loadLabel(c: Pick<RunCell, 'concurrency' | 'arrival_rate'>): string {
	if (c.arrival_rate != null) return `${c.arrival_rate} req/s`;
	return `${c.concurrency} user${c.concurrency === 1 ? '' : 's'}`;
}

export function dimValueLabel(dim: Dim, v: string | number, loadKind: 'users' | 'rate' = 'users'): string {
	switch (dim) {
		case 'load':
			return loadKind === 'rate' ? `${v} req/s` : `${v} user${v === 1 ? '' : 's'}`;
		case 'context':
			return `${formatTokens(Number(v))} tokens`;
		case 'thinking':
			return `thinking ${v}`;
		default:
			return String(v);
	}
}

export const DIM_LABELS: Record<Dim, string> = {
	entity: 'Model',
	load: 'Load',
	context: 'Context length',
	thinking: 'Thinking level'
};

/** Cells per target: the runner puts all cells of a target together. */
export function targetIndexOf(run: Run, c: RunCell): number {
	const per = Math.max(1, Math.round(run.cells.length / Math.max(1, run.config.targets.length)));
	return Math.min(run.config.targets.length - 1, Math.floor(c.index / per));
}

export function cellTargetLabel(run: Run, c: RunCell): string {
	if (c.profile_name) return c.profile_name;
	const multiSource = new Set(run.cells.map((x) => x.source_id)).size > 1;
	return multiSource ? `${c.model} (${c.source_name})` : c.model;
}

/** Normalizes a run's measured cells; entityPrefix distinguishes runs in comparisons. */
export function explorerCells(
	run: Run,
	opts: { entityPrefix?: string; entityOffset?: number; goodput?: Record<string, Goodput> | null } = {}
): ExplorerCell[] {
	const prefix = opts.entityPrefix ?? '';
	const offset = opts.entityOffset ?? 0;
	const goodput = opts.goodput ?? {};
	return run.cells
		.filter((c) => c.summary && c.summary.succeeded > 0)
		.map((c) => {
			const ti = targetIndexOf(run, c);
			const label = cellTargetLabel(run, c);
			// In comparisons a single-model run is identified by its name alone.
			const entityLabel = !prefix ? label : run.config.targets.length === 1 ? prefix : `${prefix} · ${label}`;
			return {
				entity: `${run.id}:${ti}`,
				entityLabel,
				entityIndex: offset + ti,
				load: c.arrival_rate ?? c.concurrency,
				loadKind: c.arrival_rate != null ? 'rate' : 'users',
				context: c.context_tokens,
				thinking: c.thinking,
				summary: goodput[c.id] ? { ...c.summary!, goodput: goodput[c.id] } : c.summary!,
				cell: c
			};
		});
}

export function dimValue(c: ExplorerCell, dim: Dim): string | number {
	switch (dim) {
		case 'entity':
			return c.entity;
		case 'load':
			return c.load;
		case 'context':
			return c.context;
		case 'thinking':
			return c.thinking;
	}
}

/** Distinct values of a dimension, in natural order. */
export function dimValues(cells: ExplorerCell[], dim: Dim): (string | number)[] {
	const vals = [...new Set(cells.map((c) => dimValue(c, dim)))];
	if (dim === 'thinking') return vals.sort((a, b) => THINKING_ORDER.indexOf(String(a)) - THINKING_ORDER.indexOf(String(b)));
	if (dim === 'entity') {
		const order = new Map(cells.map((c) => [c.entity, c.entityIndex]));
		return vals.sort((a, b) => (order.get(String(a)) ?? 0) - (order.get(String(b)) ?? 0));
	}
	return vals.sort((a, b) => Number(a) - Number(b));
}

export interface MetricDef {
	key: string;
	label: string;
	get: (s: Aggregate) => number | null;
	fmt: (v: number | null) => string;
	/** true: lower is better; false: higher is better. */
	lowerIsBetter: boolean;
}

const q = (s: { count: number }, v: number) => (s.count ? v : null);

export const METRICS: MetricDef[] = [
	{ key: 'output_throughput', label: 'Output throughput (tok/s)', get: (s) => s.output_throughput, fmt: (v) => rate(v, ''), lowerIsBetter: false },
	{ key: 'request_throughput', label: 'Requests per second', get: (s) => s.request_throughput, fmt: (v) => (v == null ? '–' : v.toFixed(2)), lowerIsBetter: false },
	{ key: 'ttft_p50', label: 'TTFT p50', get: (s) => q(s.ttft_ms, s.ttft_ms.p50), fmt: ms, lowerIsBetter: true },
	{ key: 'ttft_p95', label: 'TTFT p95', get: (s) => q(s.ttft_ms, s.ttft_ms.p95), fmt: ms, lowerIsBetter: true },
	{ key: 'ttft_p99', label: 'TTFT p99', get: (s) => q(s.ttft_ms, s.ttft_ms.p99), fmt: ms, lowerIsBetter: true },
	{ key: 'ttfat_p50', label: 'Time to first answer p50', get: (s) => q(s.ttfat_ms, s.ttfat_ms.p50), fmt: ms, lowerIsBetter: true },
	{ key: 'e2e_p50', label: 'End-to-end p50', get: (s) => q(s.e2e_ms, s.e2e_ms.p50), fmt: ms, lowerIsBetter: true },
	{ key: 'e2e_p95', label: 'End-to-end p95', get: (s) => q(s.e2e_ms, s.e2e_ms.p95), fmt: ms, lowerIsBetter: true },
	{ key: 'tpot_p50', label: 'TPOT p50', get: (s) => q(s.tpot_ms, s.tpot_ms.p50), fmt: ms, lowerIsBetter: true },
	{ key: 'tpot_p95', label: 'TPOT p95', get: (s) => q(s.tpot_ms, s.tpot_ms.p95), fmt: ms, lowerIsBetter: true },
	{ key: 'itl_p99', label: 'Inter-chunk latency p99', get: (s) => q(s.itl_ms, s.itl_ms.p99), fmt: ms, lowerIsBetter: true },
	{ key: 'user_tps', label: 'Output speed per user (tok/s)', get: (s) => q(s.output_tps, s.output_tps.mean), fmt: (v) => rate(v, ''), lowerIsBetter: false },
	{ key: 'prefill_tps', label: 'Prefill speed (tok/s)', get: (s) => q(s.prefill_tps, s.prefill_tps.p50), fmt: (v) => rate(v, ''), lowerIsBetter: false },
	{ key: 'output_tokens', label: 'Output tokens (total)', get: (s) => s.output_tokens, fmt: int, lowerIsBetter: false },
	{ key: 'error_rate', label: 'Error rate', get: (s) => 100 * s.error_rate, fmt: (v) => pct(v), lowerIsBetter: true },
	// Only offered when an SLO is set (see metricsFor).
	{ key: 'goodput_rps', label: 'Goodput (good requests/s)', get: (s) => s.goodput?.per_second ?? null, fmt: (v) => (v == null ? '–' : v.toFixed(2)), lowerIsBetter: false },
	{ key: 'slo_attainment', label: 'Requests within SLO (%)', get: (s) => (s.goodput?.requests ? 100 * s.goodput.ratio : null), fmt: (v) => pct(v), lowerIsBetter: false }
];

const GOODPUT_KEYS = new Set(['goodput_rps', 'slo_attainment']);

/** The metrics that have data for these cells (goodput needs an SLO). */
export function metricsFor(cells: ExplorerCell[]): MetricDef[] {
	if (cells.some((c) => c.summary.goodput)) return METRICS;
	return METRICS.filter((m) => !GOODPUT_KEYS.has(m.key));
}

export const metricByKey = (key: string) => METRICS.find((m) => m.key === key) ?? METRICS[0];

export const VIOLATION_LABELS: Record<string, string> = {
	ttft: 'TTFT',
	tpot: 'TPOT',
	e2e: 'end-to-end latency',
	output_tps: 'output speed',
	error: 'errors'
};

/** "TTFT ≤ 500 ms · TPOT ≤ 50 ms · ≥ 20 tok/s" */
export function sloSummary(slo: SLO | null | undefined): string {
	if (!slo) return '';
	const parts = [];
	if (slo.ttft_ms != null) parts.push(`TTFT ≤ ${ms(slo.ttft_ms)}`);
	if (slo.tpot_ms != null) parts.push(`TPOT ≤ ${ms(slo.tpot_ms)}`);
	if (slo.e2e_ms != null) parts.push(`E2E ≤ ${ms(slo.e2e_ms)}`);
	if (slo.min_output_tps != null) parts.push(`≥ ${rate(slo.min_output_tps)} per user`);
	return parts.join(' · ');
}

export const TYPE_LABELS: Record<string, string> = {
	single: 'Single benchmark',
	concurrency_sweep: 'Concurrency sweep',
	context_sweep: 'Context sweep',
	thinking_comparison: 'Thinking comparison',
	matrix: 'Matrix'
};

export function typeLabel(run: Pick<Run, 'type' | 'config'>): string {
	if (run.type === 'concurrency_sweep' && run.config.load_model === 'open') return 'Rate sweep';
	return TYPE_LABELS[run.type] ?? run.type;
}

/** Short description of a run's load, e.g. "1, 2, 4 users" or "5, 10 req/s". */
export function loadSummary(run: Pick<Run, 'config'>): string {
	const c = run.config;
	if (c.load_model === 'open') return `${(c.arrival_rates ?? []).join(', ')} req/s`;
	return `${(c.concurrency ?? []).join(', ')} user${(c.concurrency ?? []).length === 1 && c.concurrency[0] === 1 ? '' : 's'}`;
}

/** What each request sent, e.g. "synthetic 1,024-token prompt · max 256 output tokens". */
export function promptSummary(r: Pick<Run, 'config'>): string {
	const pr = r.config.prompt;
	let input: string;
	if (r.config.context_lengths?.length) input = `synthetic prompts at ${r.config.context_lengths.map(formatTokens).join(', ')} tokens`;
	else input = pr.mode === 'synthetic' ? `synthetic ${int(pr.input_tokens)}-token prompt` : `fixed prompt (${pr.text.length} chars)`;
	const out = r.config.max_tokens
		? `max ${int(r.config.max_tokens)} output tokens${r.config.ignore_eos ? ' (ignore EOS)' : ''}`
		: 'server-default output length';
	const parts = [input, out];
	if (r.config.thinking_levels?.length) parts.push(`thinking ${r.config.thinking_levels.join(' / ')} (${r.config.thinking_style})`);
	return parts.join(' · ');
}

/** How the load was applied and when each step ended. */
export function limitSummary(r: Pick<Run, 'config'>): string {
	const parts = [];
	if (r.config.requests_per_cell) parts.push(`${int(r.config.requests_per_cell)} requests`);
	if (r.config.duration_seconds) parts.push(`${duration(r.config.duration_seconds)}`);
	let s = `${r.config.load_model === 'open' ? `open loop at ${loadSummary(r)}, max ${r.config.max_in_flight} in flight` : `closed loop, ${loadSummary(r)}`}`;
	s += ` · ${parts.join(' or ')} per step`;
	if (r.config.warmup_requests) s += ` · ${r.config.warmup_requests} warm-up`;
	s += r.config.cache_bust ? ' · cache busting on' : ' · cache busting off';
	if (r.config.max_error_rate) s += ` · stop above ${Math.round(r.config.max_error_rate * 100)}% errors`;
	return s;
}
