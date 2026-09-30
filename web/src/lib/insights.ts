// Automatic findings: short sentences that tell the user what a run shows
// without reading the tables. Pure functions over measured cells.

import { dimValue, formatTokens, VIOLATION_LABELS, type Dim, type ExplorerCell } from './dims';
import { int, ms, rate } from './format';

export interface Finding {
	kind: 'trend' | 'best' | 'warning';
	text: string;
}

const MAX_FINDINGS = 7;

/** Groups cells that differ only in `dim`, each group sorted along it. */
function sequences(cells: ExplorerCell[], dim: Dim, varying: Dim[]): { key: string; label: string; cells: ExplorerCell[] }[] {
	const others = varying.filter((d) => d !== dim);
	const groups = new Map<string, ExplorerCell[]>();
	for (const c of cells) {
		const key = others.map((d) => String(dimValue(c, d))).join('|');
		groups.set(key, [...(groups.get(key) ?? []), c]);
	}
	const order = ['off', 'low', 'medium', 'high'];
	return [...groups.entries()].map(([key, g]) => {
		const sorted = [...g].sort((a, b) =>
			dim === 'thinking' ? order.indexOf(a.thinking) - order.indexOf(b.thinking) : Number(dimValue(a, dim)) - Number(dimValue(b, dim))
		);
		const parts: string[] = [];
		for (const d of others) {
			const c = sorted[0];
			if (d === 'entity') parts.push(c.entityLabel);
			if (d === 'context') parts.push(`${formatTokens(c.context)} tokens`);
			if (d === 'thinking') parts.push(`thinking ${c.thinking}`);
			if (d === 'load') parts.push(loadText(c));
		}
		return { key, label: parts.join(' · '), cells: sorted };
	});
}

function loadText(c: ExplorerCell): string {
	return c.loadKind === 'rate' ? `${c.load} req/s` : `${c.load} user${c.load === 1 ? '' : 's'}`;
}

function prefix(label: string): string {
	return label ? `${label}: ` : '';
}

function ratio(a: number, b: number): string {
	const r = b / a;
	return r >= 10 ? `${Math.round(r)}×` : `${r.toFixed(1)}×`;
}

export function findings(cells: ExplorerCell[], varying: Dim[]): Finding[] {
	const out: Finding[] = [];
	if (cells.length === 0) return out;

	// Each dimension gives one sentence: detailed for a single combination of
	// the other dimensions, a merged range when the pattern repeats.

	// Load: saturation point.
	if (varying.includes('load')) {
		const groups = sequences(cells, 'load', varying).filter((s) => s.cells.length >= 2);
		const results = groups.map((s) => {
			const seq = s.cells;
			const tps = seq.map((c) => c.summary.output_throughput);
			const peak = Math.max(...tps);
			const kneeIdx = tps.findIndex((t) => t >= 0.9 * peak);
			const knee = seq[kneeIdx];
			const last = seq[seq.length - 1];
			const saturated = seq.length >= 3 && kneeIdx < seq.length - 1;
			const a = knee.summary.ttft_ms.p50;
			const b = last.summary.ttft_ms.p50;
			const ttftRise = knee.summary.ttft_ms.count && last.summary.ttft_ms.count && b >= 1.5 * a ? b / a : null;
			return { s, knee, last, saturated, ttftRise, a, b };
		});
		if (results.length === 1) {
			const { s, knee, last, saturated, ttftRise, a, b } = results[0];
			if (saturated) {
				let text = `${prefix(s.label)}throughput plateaus at ${loadText(knee)} (${rate(knee.summary.output_throughput)}); more load adds little`;
				if (ttftRise) text += ` while TTFT p50 rises ${ratio(a, b)} (${ms(a)} → ${ms(b)})`;
				out.push({ kind: 'trend', text: `${text}.` });
			} else {
				out.push({ kind: 'trend', text: `${prefix(s.label)}throughput is still rising at ${loadText(last)} (${rate(last.summary.output_throughput)}): not saturated yet; try higher ${last.loadKind === 'rate' ? 'rates' : 'user counts'}.` });
			}
		} else if (results.length > 1) {
			const sat = results.filter((r) => r.saturated);
			const last = results[0].last;
			if (sat.length === 0) {
				out.push({ kind: 'trend', text: `Throughput is still rising at ${loadText(last)} in all ${results.length} combinations: not saturated yet; try higher ${last.loadKind === 'rate' ? 'rates' : 'user counts'}.` });
			} else {
				const knees = [...new Set(sat.map((r) => loadText(r.knee)))].join(' / ');
				let text =
					results.length <= 4
						? `Throughput plateaus: ${sat.map((r) => `${r.s.label} at ${loadText(r.knee)}`).join(' · ')}`
						: `Throughput plateaus at ${knees} in ${sat.length === results.length ? `all ${results.length}` : `${sat.length} of ${results.length}`} combinations`;
				const rises = sat.flatMap((r) => (r.ttftRise ? [r.ttftRise] : []));
				if (rises.length) text += `; past that, TTFT p50 rises ${rangeOf(rises, (v) => `${v.toFixed(1)}×`)}`;
				if (sat.length < results.length) text += `; the others are still rising at ${loadText(last)}`;
				out.push({ kind: 'trend', text: `${text}.` });
			}
		}
	}

	// Context length: cost of long prompts.
	if (varying.includes('context')) {
		const results = sequences(cells, 'context', varying).flatMap((s) => {
			const first = s.cells[0];
			const last = s.cells[s.cells.length - 1];
			if (first === last || !first.summary.ttft_ms.count || !last.summary.ttft_ms.count) return [];
			const sa = first.summary.output_tps.mean;
			const sb = last.summary.output_tps.mean;
			const speedDrop = first.summary.output_tps.count && last.summary.output_tps.count && sb < 0.9 * sa ? 1 - sb / sa : null;
			return [{ s, first, last, a: first.summary.ttft_ms.p50, b: last.summary.ttft_ms.p50, speedDrop }];
		});
		if (results.length === 1) {
			const { s, first, last, a, b, speedDrop } = results[0];
			let text = `${prefix(s.label)}${formatTokens(last.context)} tokens of context make TTFT ${ratio(a, b)} slower than ${formatTokens(first.context)} (${ms(a)} → ${ms(b)})`;
			if (speedDrop) text += `, and per-user output speed drops ${Math.round(100 * speedDrop)}%`;
			out.push({ kind: 'trend', text: `${text}.` });
		} else if (results.length > 1) {
			const { first, last } = results[0];
			let text = `${formatTokens(last.context)} tokens of context make TTFT ${rangeOf(results.map((r) => r.b / r.a), (v) => `${v.toFixed(1)}×`)} slower than ${formatTokens(first.context)} in all ${results.length} combinations`;
			const drops = results.flatMap((r) => (r.speedDrop ? [100 * r.speedDrop] : []));
			if (drops.length) text += `; per-user output speed drops ${rangeOf(drops, (v) => `${Math.round(v)}%`)}`;
			out.push({ kind: 'trend', text: `${text}.` });
		}
	}

	// Thinking: latency and token cost of reasoning.
	if (varying.includes('thinking')) {
		const results = sequences(cells, 'thinking', varying).flatMap((s) => {
			const base = s.cells[0];
			const top = s.cells[s.cells.length - 1];
			if (base === top || !base.summary.ttfat_ms.count || !top.summary.ttfat_ms.count) return [];
			return [{
				s,
				base,
				top,
				delta: top.summary.ttfat_ms.p50 - base.summary.ttfat_ms.p50,
				e2e: base.summary.e2e_ms.p50 ? (100 * (top.summary.e2e_ms.p50 - base.summary.e2e_ms.p50)) / base.summary.e2e_ms.p50 : 0,
				reasoning: top.summary.succeeded ? top.summary.reasoning_tokens / top.summary.succeeded : 0
			}];
		});
		const signed = (v: number) => `${v >= 0 ? '+' : '−'}${Math.abs(Math.round(v))}%`;
		if (results.length === 1) {
			const { s, base, top, delta, e2e, reasoning } = results[0];
			let text = `${prefix(s.label)}thinking ${top.thinking} adds ${ms(delta)} before the first answer token versus ${base.thinking} (end-to-end ${signed(e2e)})`;
			if (reasoning >= 1) text += `, with ~${int(Math.round(reasoning))} reasoning tokens per request`;
			out.push({ kind: 'trend', text: `${text}.` });
		} else if (results.length > 1) {
			const { base, top } = results[0];
			let text = `Thinking ${top.thinking} adds ${rangeOf(results.map((r) => r.delta), (v) => ms(v))} before the first answer token versus ${base.thinking} (end-to-end ${rangeOf(results.map((r) => r.e2e), signed)}) across ${results.length} combinations`;
			const tokens = results.filter((r) => r.reasoning >= 1).map((r) => r.reasoning);
			if (tokens.length) text += `, with ~${rangeOf(tokens, (v) => int(Math.round(v)))} reasoning tokens per request`;
			out.push({ kind: 'trend', text: `${text}.` });
		}
	}

	// Several models: who leads.
	const entities = [...new Set(cells.map((c) => c.entity))];
	if (entities.length > 1) {
		const peakBy = entities
			.map((e) => {
				const own = cells.filter((c) => c.entity === e);
				const best = own.reduce((a, b) => (b.summary.output_throughput > a.summary.output_throughput ? b : a));
				return { label: best.entityLabel, value: best.summary.output_throughput };
			})
			.sort((a, b) => b.value - a.value);
		const [first, second] = peakBy;
		if (second.value > 0) {
			out.push({
				kind: 'best',
				text: `${first.label} has the highest peak throughput (${rate(first.value)}), ${Math.round((100 * (first.value - second.value)) / second.value)}% above ${second.label}.`
			});
		}
		const withTTFT = cells.filter((c) => c.summary.ttft_ms.count);
		if (withTTFT.length) {
			const fastest = withTTFT.reduce((a, b) => (b.summary.ttft_ms.p50 < a.summary.ttft_ms.p50 ? b : a));
			out.push({ kind: 'best', text: `${fastest.entityLabel} starts answering fastest: best TTFT p50 ${ms(fastest.summary.ttft_ms.p50)}.` });
		}
	}

	// Sentences without a model prefix start lowercase; capitalize them.
	return out.slice(0, MAX_FINDINGS).map((f) => ({ ...f, text: f.text.charAt(0).toUpperCase() + f.text.slice(1) }));
}

/** Errors across all cells (including cells with no successful request). */
export function errorFinding(summaries: { failed: number; requests: number; errors_by_type: Record<string, number> }[]): Finding | null {
	const failed = summaries.reduce((n, s) => n + s.failed, 0);
	const total = summaries.reduce((n, s) => n + s.requests, 0);
	if (!failed) return null;
	const byType: Record<string, number> = {};
	for (const s of summaries) for (const [k, v] of Object.entries(s.errors_by_type)) byType[k] = (byType[k] ?? 0) + v;
	const [topType, topCount] = Object.entries(byType).sort((a, b) => b[1] - a[1])[0] ?? ['error', failed];
	return {
		kind: 'warning',
		text: `${int(failed)} of ${int(total)} requests failed (${((100 * failed) / total).toFixed(1)}%); most common: ${topType} (${int(topCount)}).`
	};
}

export interface Winner {
	label: string;
	value: string;
	/** '' for a tie. */
	who: string;
	margin: string;
}

const minOf = (xs: number[]) => (xs.length ? Math.min(...xs) : null);

/** Comparison headline: each series' best step per metric, then the best series. */
export function winnerTiles(cells: ExplorerCell[]): Winner[] {
	const entities = [...new Map(cells.map((c) => [c.entity, c.entityLabel])).entries()];
	if (entities.length < 2) return [];
	const defs: { label: string; lower: boolean; fmt: (v: number) => string; of: (own: ExplorerCell[]) => number | null }[] = [
		{ label: 'Peak output throughput', lower: false, fmt: (v) => rate(v), of: (own) => Math.max(...own.map((c) => c.summary.output_throughput)) },
		{ label: 'Best TTFT (p50)', lower: true, fmt: (v) => ms(v), of: (own) => minOf(own.filter((c) => c.summary.ttft_ms.count).map((c) => c.summary.ttft_ms.p50)) },
		{ label: 'Best end-to-end (p50)', lower: true, fmt: (v) => ms(v), of: (own) => minOf(own.map((c) => c.summary.e2e_ms.p50)) },
		{
			label: 'Error rate',
			lower: true,
			fmt: (v) => `${v.toFixed(v < 1 ? 2 : 1)}%`,
			of: (own) => {
				const req = own.reduce((n, c) => n + c.summary.requests, 0);
				return req ? (100 * own.reduce((n, c) => n + c.summary.failed, 0)) / req : null;
			}
		}
	];
	if (cells.some((c) => c.summary.goodput)) {
		defs.push({
			label: 'Peak goodput',
			lower: false,
			fmt: (v) => `${v.toFixed(2)} req/s`,
			of: (own) => {
				const vals = own.flatMap((c) => (c.summary.goodput ? [c.summary.goodput.per_second] : []));
				return vals.length ? Math.max(...vals) : null;
			}
		});
	}
	return defs.flatMap((d) => {
		const ranked = entities
			.map(([key, label]) => ({ label, v: d.of(cells.filter((c) => c.entity === key)) }))
			.filter((x): x is { label: string; v: number } => x.v != null && Number.isFinite(x.v))
			.sort((a, b) => (d.lower ? a.v - b.v : b.v - a.v));
		if (ranked.length < 2) return [];
		const [first, second] = ranked;
		// Within 1% is a tie: no winner is named.
		if (Math.abs(first.v - second.v) <= 0.01 * Math.max(Math.abs(first.v), Math.abs(second.v))) {
			return [{ label: d.label, value: d.fmt(first.v), who: '', margin: `${first.label} and ${second.label} are tied` }];
		}
		const margin = d.lower
			? second.v > 0
				? `${Math.round((100 * (second.v - first.v)) / second.v)}% lower than ${second.label}`
				: ''
			: second.v > 0
				? `${Math.round((100 * (first.v - second.v)) / second.v)}% above ${second.label}`
				: '';
		return [{ label: d.label, value: d.fmt(first.v), who: first.label, margin }];
	});
}

/** Share of requests that must meet the SLO for a step to count as within it. */
export const SLO_ATTAINMENT = 0.9;

function topViolation(cells: ExplorerCell[]): string {
	const counts: Record<string, number> = {};
	for (const c of cells) for (const [k, v] of Object.entries(c.summary.goodput?.violations ?? {})) counts[k] = (counts[k] ?? 0) + v;
	const top = Object.entries(counts).sort((a, b) => b[1] - a[1])[0];
	return top ? (VIOLATION_LABELS[top[0]] ?? top[0]) : '';
}

/** Capacity under the SLO for one load sequence: the highest load before the first step that misses it. */
export function sloCapacity(seq: ExplorerCell[]): { within: ExplorerCell | null; firstMiss: ExplorerCell | null } {
	let within: ExplorerCell | null = null;
	for (const c of seq) {
		const g = c.summary.goodput;
		if (!g || !g.requests) continue;
		if (g.ratio < SLO_ATTAINMENT) return { within, firstMiss: c };
		within = c;
	}
	return { within, firstMiss: null };
}

/** What the SLO says about a run: capacity along load, or overall attainment. */
export function goodputFindings(cells: ExplorerCell[], varying: Dim[]): Finding[] {
	const measured = cells.filter((c) => c.summary.goodput?.requests);
	if (!measured.length) return [];
	const out: Finding[] = [];
	const pctOf = (r: number) => `${Math.round(100 * r)}%`;
	const bar = `≥${pctOf(SLO_ATTAINMENT)} of requests`;

	if (varying.includes('load')) {
		const groups = sequences(measured, 'load', varying).filter((s) => s.cells.length >= 2);
		const results = groups.map((s) => ({ s, ...sloCapacity(s.cells) }));
		if (results.length === 1) {
			const { s, within, firstMiss } = results[0];
			if (!within) {
				const first = s.cells[0];
				out.push({
					kind: 'warning',
					text: `${prefix(s.label)}even at ${loadText(first)} only ${pctOf(first.summary.goodput!.ratio)} of requests meet the SLO; most misses: ${topViolation(s.cells)}.`
				});
			} else if (!firstMiss) {
				out.push({
					kind: 'best',
					text: `${prefix(s.label)}every load level meets the SLO (${bar}), up to ${loadText(within)}: capacity is higher; try more load.`
				});
			} else {
				out.push({
					kind: 'best',
					text: `${prefix(s.label)}meets the SLO (${bar}) up to ${loadText(within)}; at ${loadText(firstMiss)} only ${pctOf(firstMiss.summary.goodput!.ratio)} do (most misses: ${topViolation([firstMiss])}).`
				});
			}
		} else if (results.length > 1) {
			const caps = results.filter((r) => r.within);
			if (caps.length === 0) {
				out.push({ kind: 'warning', text: `No combination meets the SLO (${bar}) even at the lowest load; most misses: ${topViolation(measured)}.` });
			} else if (results.length <= 4) {
				// Few enough to name each one's capacity.
				const parts = results.map((r) => {
					if (!r.within) return `${r.s.label} not even at ${loadText(r.s.cells[0])}`;
					return `${r.s.label} up to ${loadText(r.within)}${r.firstMiss ? '' : ' (all levels)'}`;
				});
				out.push({ kind: 'best', text: `Meets the SLO (${bar}): ${parts.join(' · ')}.` });
			} else {
				const loads = [...new Set(caps.map((r) => loadText(r.within!)))].join(' / ');
				let text = `Meets the SLO (${bar}) up to ${loads} in ${caps.length === results.length ? `all ${results.length}` : `${caps.length} of ${results.length}`} combinations`;
				if (caps.length < results.length) text += '; the others miss it even at the lowest load';
				out.push({ kind: 'best', text: `${text}.` });
			}
		}
	} else {
		const good = measured.reduce((n, c) => n + c.summary.goodput!.good, 0);
		const total = measured.reduce((n, c) => n + c.summary.goodput!.requests, 0);
		const r = good / total;
		out.push({
			kind: r >= SLO_ATTAINMENT ? 'best' : 'warning',
			text: `${pctOf(r)} of requests meet the SLO${r < 1 ? `; most misses: ${topViolation(measured)}` : ''}.`
		});
	}

	// Several models: who delivers the most good requests per second.
	const entities = [...new Set(measured.map((c) => c.entity))];
	if (entities.length > 1) {
		const best = measured.reduce((a, b) => (b.summary.goodput!.per_second > a.summary.goodput!.per_second ? b : a));
		if (best.summary.goodput!.per_second > 0) {
			out.push({ kind: 'best', text: `${best.entityLabel} has the highest goodput: ${best.summary.goodput!.per_second.toFixed(2)} good requests/s.` });
		}
	}
	return out.map((f) => ({ ...f, text: f.text.charAt(0).toUpperCase() + f.text.slice(1) }));
}

/** "a" or "a–b" for a list of values, formatted. */
function rangeOf(values: number[], fmt: (v: number) => string): string {
	const lo = Math.min(...values);
	const hi = Math.max(...values);
	const a = fmt(lo);
	const b = fmt(hi);
	return a === b ? a : `${a}–${b}`;
}
