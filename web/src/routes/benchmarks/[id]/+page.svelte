<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { untrack } from 'svelte';
	import {
		ApiError,
		deleteRun,
		exportRunCsvUrl,
		exportRunsUrl,
		getDefinition,
		getRun,
		getRunGoodput,
		listRequests,
		runEventsUrl,
		stopRun,
		updateRunSLO
	} from '$lib/api';
	import {
		cellTargetLabel,
		explorerCells,
		formatTokens,
		limitSummary,
		loadLabel,
		promptSummary,
		sloSummary,
		targetIndexOf,
		typeLabel,
		VIOLATION_LABELS
	} from '$lib/dims';
	import { downloadText, downloadUrl, safeFilename } from '$lib/download';
	import { dateTime, duration, int, ms, pct, rate } from '$lib/format';
	import { SLO_ATTAINMENT } from '$lib/insights';
	import { seriesColor } from '$lib/palette';
	import { runReport } from '$lib/report';
	import type { Aggregate, Goodput, LivePoint, LiveUpdate, RequestRecord, Run, RunCell, SLO, Summary } from '$lib/types';
	import Markdown from '$lib/components/Markdown.svelte';
	import RequestDetail from '$lib/components/RequestDetail.svelte';
	import ResultsExplorer from '$lib/components/ResultsExplorer.svelte';
	import RunOverview from '$lib/components/RunOverview.svelte';
	import RunStatus from '$lib/components/RunStatus.svelte';
	import TimeChart, { type Marker } from '$lib/components/TimeChart.svelte';

	const CPU_WARN_PCT = 80;
	const LAG_WARN_MS = 100;
	// Charts to open with, by run type.
	const DEFAULT_METRICS: Record<string, [string, string]> = {
		thinking_comparison: ['ttfat_p50', 'e2e_p50'],
		context_sweep: ['ttft_p50', 'user_tps'],
		default: ['output_throughput', 'ttft_p95']
	};
	const REQ_PAGE = 25;

	let run = $state<Run>();
	let error = $state('');
	let expanded = $state<string | null>(null);
	let detailId = $state<string | null>(null);

	let reqCell = $state('');
	let reqStatus = $state('');
	let reqOffset = $state(0);
	let requests = $state<RequestRecord[]>([]);
	let reqTotal = $state(0);

	/** Goodput per cell under `evaluatedSLO` (the saved targets or a what-if preview). */
	let goodput = $state<Record<string, Goodput> | null>(null);
	let evaluatedSLO = $state<SLO | null>(null);

	const runId = $derived(page.params.id ?? '');

	/** Live dashboard series: streamed while running, persisted afterwards. */
	let livePoints = $state.raw<LivePoint[]>([]);

	// Load, then follow the live event stream while the run is in progress.
	$effect(() => {
		const id = runId;
		let alive = true;
		let es: EventSource | null = null;

		async function load() {
			try {
				const r = await getRun(id);
				if (!alive) return;
				run = r;
				livePoints = r.timeline ?? [];
				error = '';
				if (r.status === 'running') connect();
			} catch (e) {
				if (alive) error = (e as Error).message;
			}
		}

		function connect() {
			es = new EventSource(runEventsUrl(id));
			// Each (re)connection starts with the full series.
			let replace = true;
			es.addEventListener('update', (ev) => {
				const u: LiveUpdate = JSON.parse((ev as MessageEvent).data);
				run = u.run;
				livePoints = replace ? u.points : [...livePoints, ...u.points];
				replace = false;
			});
			es.addEventListener('done', () => {
				es?.close();
				load();
			});
			es.onerror = () => {
				replace = true;
			};
		}

		load();
		return () => {
			alive = false;
			es?.close();
		};
	});

	// Requests table follows filters, and refreshes as cells finish.
	const finishedCells = $derived(run?.cells.filter((c) => c.finished_at).length ?? 0);
	$effect(() => {
		const f = { run_id: runId, cell_id: reqCell, status: reqStatus, offset: reqOffset, limit: REQ_PAGE };
		void finishedCells;
		listRequests(f)
			.then((r) => {
				requests = r.requests;
				reqTotal = r.total;
			})
			.catch(() => {});
	});

	// Each model/profile keeps its color everywhere (config order).
	const colorOf = (c: RunCell) => (run ? seriesColor(targetIndexOf(run, c)) : seriesColor(0));
	const cellById = $derived(new Map(run?.cells.map((c) => [c.id, c]) ?? []));
	const cellLabel = (c: RunCell) => (run ? cellTargetLabel(run, c) : c.model);

	// Which dimensions this run varies, for table columns and labels.
	const sweepsContext = $derived(!!run?.config.context_lengths?.length);
	const sweepsThinking = $derived(!!run?.config.thinking_levels?.length);
	const openLoop = $derived(run?.config.load_model === 'open');
	/** Everything that distinguishes a cell besides the model, e.g. "4k tokens · thinking high · 8 users". */
	function cellDims(c: RunCell): string {
		const parts = [];
		if (sweepsContext) parts.push(`${formatTokens(c.context_tokens)} tokens`);
		if (sweepsThinking) parts.push(`thinking ${c.thinking}`);
		// The load is only worth repeating when it varies (or is all there is).
		if (sweepsLoad || parts.length === 0) parts.push(loadLabel(c));
		return parts.join(' · ');
	}
	const sweepsLoad = $derived(!!run && (run.config.load_model === 'open' ? run.config.arrival_rates : run.config.concurrency).length > 1);

	const explorer = $derived(run ? explorerCells(run, { goodput }) : []);
	const showExplorer = $derived(!!run && run.cells.length > 1 && explorer.length > 0);
	const samples = $derived(run?.cells.filter((c) => c.sample_output || c.sample_reasoning) ?? []);

	// Live dashboard series and step markers.
	const liveMarkers = $derived.by((): Marker[] => {
		const out: Marker[] = [];
		let prev = -1;
		for (const p of livePoints) {
			if (p.cell !== prev) {
				const c = run?.cells[p.cell];
				if (c && prev !== -1) {
					const dims = sweepsContext || sweepsThinking ? cellDims(c) : loadLabel(c);
					out.push({ t: p.t, label: run!.config.targets.length > 1 ? `${cellLabel(c)} · ${dims}` : dims });
				}
				prev = p.cell;
			}
		}
		return out;
	});
	const liveThroughput = $derived([
		{ name: 'Output tok/s', color: seriesColor(0), data: livePoints.map((p) => [p.t, p.tps] as [number, number]) }
	]);
	const liveTTFT = $derived([
		{ name: 'TTFT p50', color: seriesColor(0), data: livePoints.map((p) => [p.t, p.ttft_p50] as [number, number | null]) },
		{ name: 'TTFT p95', color: seriesColor(1), data: livePoints.map((p) => [p.t, p.ttft_p95] as [number, number | null]) }
	]);
	const liveLoad = $derived([
		{ name: 'Active users', color: seriesColor(0), data: livePoints.map((p) => [p.t, p.users] as [number, number]) },
		{ name: 'In flight', color: seriesColor(1), data: livePoints.map((p) => [p.t, p.in_flight] as [number, number]) }
	]);
	const liveErrors = $derived(livePoints.reduce((n, p) => n + p.errors, 0));

	const lagWarnings = $derived(
		run?.cells.filter((c) => c.schedule_lag_p95_ms != null && c.schedule_lag_p95_ms >= LAG_WARN_MS) ?? []
	);

	const cpuWarnings = $derived(
		run?.cells.filter((c) => c.client_cpu_pct != null && c.client_cpu_pct >= CPU_WARN_PCT) ?? []
	);

	const p = $derived(run?.progress);
	const progressPct = $derived.by(() => {
		if (!p) return 0;
		if (p.phase === 'warmup') return 0;
		const byCount = p.target_requests ? p.completed / p.target_requests : 0;
		const byTime = p.duration_seconds ? p.cell_elapsed_seconds / p.duration_seconds : 0;
		return Math.min(100, 100 * Math.max(byCount, byTime));
	});
	const currentCell = $derived(p ? cellById.get(p.cell_id) : undefined);

	async function stop() {
		if (!run) return;
		try {
			await stopRun(run.id);
			run = await getRun(run.id);
		} catch (e) {
			error = (e as Error).message;
		}
	}

	async function remove() {
		if (!run || !confirm(`Delete "${run.name}" and all its ${int(run.cells.reduce((n, c) => n + (c.summary?.requests ?? 0), 0))}+ stored requests?`)) return;
		try {
			await deleteRun(run.id);
			goto('/benchmarks');
		} catch (e) {
			error = (e as Error).message;
		}
	}

	// --- goodput / SLO ---

	let sloEditing = $state(false);
	let sloForm = $state({ ttft_ms: '', tpot_ms: '', e2e_ms: '', min_output_tps: '' });
	let sloError = $state('');
	let sloSaving = $state(false);

	const savedSLO = $derived(run?.config.slo ?? null);
	const formSLO = $derived.by((): SLO | null | 'invalid' => {
		const slo: SLO = {};
		for (const [k, text] of Object.entries(sloForm) as [keyof SLO, string][]) {
			if (String(text).trim() === '') continue;
			const v = Number(text);
			if (!Number.isFinite(v) || v <= 0) return 'invalid';
			slo[k] = v;
		}
		return Object.keys(slo).length ? slo : null;
	});
	const activeSLO = $derived(sloEditing ? (formSLO === 'invalid' ? null : formSLO) : savedSLO);
	// A string key, so live updates of `run` don't re-trigger evaluation.
	const sloKey = $derived(activeSLO ? JSON.stringify(activeSLO) : '');
	const whatIf = $derived(sloEditing && sloKey !== (savedSLO ? JSON.stringify(normalizeSLO(savedSLO)) : ''));
	const runLoaded = $derived(!!run);

	function normalizeSLO(s: SLO): SLO {
		const out: SLO = {};
		for (const k of ['ttft_ms', 'tpot_ms', 'e2e_ms', 'min_output_tps'] as const) if (s[k] != null) out[k] = s[k];
		return out;
	}

	$effect(() => {
		const id = runId;
		const key = sloKey;
		void finishedCells;
		if (!runLoaded || !key) {
			goodput = null;
			evaluatedSLO = null;
			return;
		}
		const timer = setTimeout(
			() =>
				getRunGoodput(id, JSON.parse(key))
					.then((g) => {
						if (key !== sloKey) return;
						goodput = g.cells;
						evaluatedSLO = g.slo;
						sloError = '';
					})
					.catch((e) => (sloError = (e as Error).message)),
			untrack(() => sloEditing) ? 300 : 0
		);
		return () => clearTimeout(timer);
	});

	function editSLO() {
		const s = savedSLO ?? {};
		sloForm = {
			ttft_ms: s.ttft_ms == null ? '' : String(s.ttft_ms),
			tpot_ms: s.tpot_ms == null ? '' : String(s.tpot_ms),
			e2e_ms: s.e2e_ms == null ? '' : String(s.e2e_ms),
			min_output_tps: s.min_output_tps == null ? '' : String(s.min_output_tps)
		};
		sloError = '';
		sloEditing = true;
	}

	async function saveSLO(slo: SLO | null) {
		if (!run) return;
		sloSaving = true;
		try {
			run = await updateRunSLO(run.id, slo);
			sloEditing = false;
			sloError = '';
		} catch (e) {
			sloError = (e as Error).message;
		} finally {
			sloSaving = false;
		}
	}

	function attainmentClass(g: Goodput): string {
		if (g.ratio >= SLO_ATTAINMENT) return 'text-green-800';
		return g.ratio >= SLO_ATTAINMENT / 2 ? 'text-amber-800' : 'text-red-700';
	}

	function violationsText(g: Goodput): string {
		return Object.entries(g.violations)
			.sort((a, b) => b[1] - a[1])
			.map(([k, v]) => `${VIOLATION_LABELS[k] ?? k} × ${int(v)}`)
			.join(', ');
	}

	// --- export ---

	function exportReport(format: 'html' | 'md') {
		if (!run) return;
		const text = runReport(format, { run, cells: explorer, goodput, slo: evaluatedSLO });
		downloadText(`${safeFilename(run.name)}.${format}`, text, format === 'html' ? 'text/html' : 'text/markdown');
	}

	// The saved definition this run came from (it may have been deleted since).
	let definitionName = $state<string | null>(null);
	const definitionId = $derived(run?.definition_id ?? '');
	$effect(() => {
		const id = definitionId;
		if (!id) return;
		getDefinition(id)
			.then((d) => (definitionName = d.name))
			// Only a 404 means deleted; other failures just leave the name out.
			.catch((e) => (definitionName = e instanceof ApiError && e.status === 404 ? '' : null));
	});

	const summaryRows: [string, keyof Aggregate, (v: number) => string][] = [
		['Time to first token', 'ttft_ms', ms],
		['Time to first answer', 'ttfat_ms', ms],
		['End-to-end latency', 'e2e_ms', ms],
		['Time per output token', 'tpot_ms', ms],
		['Inter-chunk latency (per chunk)', 'itl_ms', ms],
		['Output speed per user', 'output_tps', (v) => rate(v)],
		['Prefill speed', 'prefill_tps', (v) => rate(v)]
	];
	const statCols = ['min', 'mean', 'p50', 'p90', 'p95', 'p99', 'max'] as const;
	const selectClass = 'rounded-md border border-stone-300 bg-white px-2 py-1 text-xs';
	const menuItem = 'block w-full px-3 py-1.5 text-left hover:bg-stone-100';
	const menuHint = 'block text-xs text-stone-400';
	const sloInput = 'w-24 rounded-md border border-stone-300 bg-white px-2 py-1 text-sm';
</script>

<div class="h-full overflow-y-auto">
	<div class="mx-auto max-w-7xl space-y-6 p-6">
		<a href="/benchmarks" class="text-xs text-stone-500 hover:text-stone-800">← Benchmarks</a>

		{#if error}
			<p class="rounded bg-red-50 px-3 py-2 text-sm text-red-800">⚠ {error}</p>
		{/if}

		{#if !run}
			{#if !error}<p class="text-sm text-stone-500">Loading…</p>{/if}
		{:else}
			<!-- Header -->
			<div class="flex flex-wrap items-start justify-between gap-4">
				<div>
					<div class="flex items-center gap-3">
						<h1 class="text-xl font-semibold">{run.name}</h1>
						<RunStatus status={run.status} />
						{#if run.imported_at}
							<span class="rounded bg-violet-50 px-1.5 py-0.5 text-xs text-violet-800" title="Imported {dateTime(run.imported_at)}">imported</span>
						{/if}
					</div>
					{#if run.definition_id}
						<p class="mt-0.5 text-xs text-stone-500">
							From saved definition
							{#if definitionName}
								<a href="/benchmarks/new?def={run.definition_id}" class="text-blue-700 hover:underline">{definitionName}</a>
							{:else if definitionName === ''}
								<span class="italic">{run.imported_at ? '(not in this instance)' : '(deleted)'}</span>
							{/if}
							v{run.definition_version}
						</p>
					{/if}
					<p class="mt-1 text-sm text-stone-500">
						{typeLabel(run)} · started {dateTime(run.created_at)}
						{#if run.started_at && run.finished_at}
							· took {duration((new Date(run.finished_at).getTime() - new Date(run.started_at).getTime()) / 1000)}
						{/if}
					</p>
					<p class="text-xs text-stone-500">{promptSummary(run)}</p>
					<p class="text-xs text-stone-500">{limitSummary(run)}</p>
				</div>
				<div class="flex gap-2 text-sm">
					{#if run.status === 'running'}
						<button class="rounded-md bg-stone-800 px-4 py-1.5 font-medium text-white hover:bg-stone-700" onclick={stop}>Stop</button>
					{:else}
						<details class="relative">
							<summary class="cursor-pointer list-none rounded-md border border-stone-300 bg-white px-3 py-1.5 hover:bg-stone-50">Export ▾</summary>
							<div class="absolute right-0 z-20 mt-1 w-64 rounded-md border border-stone-200 bg-white py-1 shadow-lg">
								<button class={menuItem} onclick={() => exportReport('html')}>HTML report <span class={menuHint}>charts, findings and tables, for sharing</span></button>
								<button class={menuItem} onclick={() => exportReport('md')}>Markdown summary <span class={menuHint}>for issues, wikis, PRs</span></button>
								<button class={menuItem} onclick={() => downloadUrl(exportRunsUrl([run!.id]))}>JSON <span class={menuHint}>everything, re-importable into LLMBench</span></button>
								<button class={menuItem} onclick={() => downloadUrl(exportRunCsvUrl(run!.id, 'requests'))}>CSV: requests <span class={menuHint}>one row per request</span></button>
								<button class={menuItem} onclick={() => downloadUrl(exportRunCsvUrl(run!.id, 'cells'))}>CSV: steps <span class={menuHint}>one row per step, all percentiles</span></button>
								{#if whatIf}<p class="border-t border-stone-100 px-3 pt-1.5 text-xs text-amber-800">CSV and JSON use the saved SLO, not the preview.</p>{/if}
							</div>
						</details>
						<a href="/benchmarks/new?from={run.id}" class="rounded-md border border-stone-300 bg-white px-3 py-1.5 hover:bg-stone-50">Re-run</a>
						<button class="rounded-md px-3 py-1.5 text-red-700 hover:bg-red-50" onclick={remove}>Delete</button>
					{/if}
				</div>
			</div>

			{#if run.error}
				<p class="rounded bg-amber-50 px-3 py-2 text-sm text-amber-900">⚠ {run.error}</p>
			{/if}

			<!-- Live progress -->
			{#if p}
				<section class="rounded-lg border border-blue-200 bg-white p-5">
					<div class="mb-2 flex flex-wrap items-baseline justify-between gap-2 text-sm">
						<span>
							<b>Step {p.cell_index + 1} of {p.cell_count}</b>
							{#if currentCell}· <span class="font-mono">{cellLabel(currentCell)}</span> · <b>{cellDims(currentCell)}</b>{/if}
						</span>
						<span class="text-stone-500">
							{#if p.phase === 'warmup'}Warming up ({p.warmup_done}/{p.warmup_total}){:else}Measuring · {duration(p.cell_elapsed_seconds)}{/if}
							· total {duration(p.run_elapsed_seconds)}
						</span>
					</div>
					<div class="mb-4 h-1.5 overflow-hidden rounded bg-blue-100" role="progressbar" aria-valuenow={Math.round(progressPct)} aria-valuemin="0" aria-valuemax="100">
						<div class="h-full rounded bg-blue-600 transition-all" style="width: {progressPct}%"></div>
					</div>
					<dl class="grid grid-cols-4 gap-4 sm:grid-cols-7">
						{#each [
							['Active users', int(p.active_users)],
							['In flight', int(p.in_flight)],
							['Completed', p.target_requests ? `${int(p.completed)} / ${int(p.target_requests)}` : int(p.completed)],
							['Failed', int(p.failed)],
							['Requests/s', p.requests_per_second.toFixed(2)],
							['Output tok/s', rate(p.output_tokens_per_second, '')],
							['Step elapsed', duration(p.cell_elapsed_seconds)]
						] as [k, v] (k)}
							<div>
								<dt class="text-xs text-stone-500">{k}</dt>
								<dd class="font-mono text-lg font-semibold text-stone-900">{v}</dd>
							</div>
						{/each}
					</dl>
					{#if p.recent_errors.length}
						<div class="mt-4 rounded bg-red-50 px-3 py-2 text-xs text-red-800">
							<div class="mb-1 font-medium">Latest errors</div>
							{#each p.recent_errors as e, i (i)}<div class="truncate font-mono" title={e}>{e}</div>{/each}
						</div>
					{/if}
				</section>
			{/if}

			<!-- Goodput targets -->
			{#if explorer.length}
				<section class="rounded-lg border px-5 py-3 text-sm {sloEditing ? 'border-emerald-300 bg-white' : 'border-stone-200 bg-white'}" aria-label="Goodput targets">
					{#if sloEditing}
						<div class="flex flex-wrap items-end gap-3">
							<div class="mr-2">
								<h2 class="font-semibold">Goodput targets (SLO)</h2>
								<p class="text-xs text-stone-500">Results update as you type. Leave a field empty to skip it.</p>
							</div>
							{#each [['ttft_ms', 'Max TTFT (ms)'], ['tpot_ms', 'Max TPOT (ms)'], ['e2e_ms', 'Max E2E (ms)'], ['min_output_tps', 'Min tok/s per user']] as [key, label] (key)}
								<label class="block">
									<span class="mb-0.5 block text-xs text-stone-600">{label}</span>
									<input class={sloInput} type="number" min="0" step="any" placeholder="—" bind:value={sloForm[key as keyof typeof sloForm]} />
								</label>
							{/each}
							<div class="ml-auto flex items-center gap-2">
								{#if whatIf}<span class="rounded bg-amber-50 px-1.5 py-0.5 text-xs text-amber-800">preview, not saved</span>{/if}
								<button class="rounded-md px-3 py-1.5 text-stone-600 hover:bg-stone-100" onclick={() => (sloEditing = false)}>Cancel</button>
								{#if savedSLO}
									<button class="rounded-md px-3 py-1.5 text-red-700 hover:bg-red-50 disabled:opacity-50" disabled={sloSaving} onclick={() => saveSLO(null)}>Remove</button>
								{/if}
								<button
									class="rounded-md bg-emerald-700 px-4 py-1.5 font-medium text-white hover:bg-emerald-800 disabled:opacity-50"
									disabled={sloSaving || formSLO === 'invalid' || !formSLO}
									onclick={() => formSLO !== 'invalid' && saveSLO(formSLO)}
								>
									Save to run
								</button>
							</div>
						</div>
						{#if formSLO === 'invalid'}<p class="mt-2 text-xs text-red-700">Targets must be positive numbers.</p>{/if}
					{:else if savedSLO}
						<div class="flex flex-wrap items-center gap-x-4 gap-y-1">
							<h2 class="font-semibold">Goodput targets</h2>
							<span class="text-stone-700">{sloSummary(savedSLO)}</span>
							<span class="text-xs text-stone-400">a request is good when it succeeds and meets all of them</span>
							{#if run.status !== 'running'}
								<button class="ml-auto rounded-md px-3 py-1 text-xs text-emerald-800 hover:bg-emerald-50" onclick={editSLO}>Edit / try other targets</button>
							{/if}
						</div>
					{:else}
						<div class="flex flex-wrap items-center gap-x-4 gap-y-1">
							<h2 class="font-semibold">Goodput targets</h2>
							<span class="text-stone-500">
								None set. Add latency targets to see <b class="font-medium">goodput</b>: how many requests meet them, and the highest load that still does.
							</span>
							{#if run.status !== 'running'}
								<button class="ml-auto rounded-md border border-emerald-300 px-3 py-1 text-xs font-medium text-emerald-800 hover:bg-emerald-50" onclick={editSLO}>Set targets</button>
							{/if}
						</div>
					{/if}
					{#if sloError}<p class="mt-2 text-xs text-red-700">⚠ {sloError}</p>{/if}
				</section>
			{/if}

			<!-- At a glance -->
			{#if explorer.length}
				<RunOverview {run} cells={explorer} cellDims={(c) => cellDims(c.cell)} slo={goodput ? evaluatedSLO : null} />
			{/if}

			{#if cpuWarnings.length}
				<p class="rounded bg-amber-50 px-3 py-2 text-sm text-amber-900">
					⚠ The load generator used {pct(Math.max(...cpuWarnings.map((c) => c.client_cpu_pct ?? 0)), 0)} of this machine's CPU
					during {cpuWarnings.length} step{cpuWarnings.length === 1 ? '' : 's'}. Results there may be limited by LLMBench, not the server.
				</p>
			{/if}

			<!-- Live dashboard (streamed while running, kept afterwards) -->
			{#if livePoints.length > 1}
				<details class="rounded-lg border border-stone-200 bg-white" open={run.status === 'running'}>
					<summary class="cursor-pointer px-5 py-3 font-semibold">
						{run.status === 'running' ? 'Live' : 'Timeline'}
						<span class="ml-2 text-xs font-normal text-stone-500">
							per-second metrics over {duration(livePoints.at(-1)?.t ?? 0)}{#if liveErrors} · {int(liveErrors)} errors{/if}
						</span>
					</summary>
					<div class="grid grid-cols-1 gap-4 px-5 pb-5 lg:grid-cols-3">
						<div>
							<h3 class="text-sm font-medium">Output tokens per second</h3>
							<p class="text-xs text-stone-500">All users combined, from requests completing each second.</p>
							<TimeChart series={liveThroughput} markers={liveMarkers} format={(v) => rate(v, '')} ariaLabel="Output tokens per second over time" />
						</div>
						<div>
							<h3 class="text-sm font-medium">Time to first token</h3>
							<p class="text-xs text-stone-500">Of requests completing each second.</p>
							<TimeChart series={liveTTFT} markers={liveMarkers} format={ms} ariaLabel="Time to first token over time" />
						</div>
						<div>
							<h3 class="text-sm font-medium">Load</h3>
							<p class="text-xs text-stone-500">Virtual users and requests awaiting a response.</p>
							<TimeChart series={liveLoad} markers={liveMarkers} step ariaLabel="Active users and requests in flight over time" />
						</div>
					</div>
				</details>
			{/if}

			{#if lagWarnings.length}
				<p class="rounded bg-amber-50 px-3 py-2 text-sm text-amber-900">
					⚠ In {lagWarnings.length} step{lagWarnings.length === 1 ? '' : 's'}, requests went out up to
					{ms(Math.max(...lagWarnings.map((c) => c.schedule_lag_p95_ms ?? 0)))} (p95) later than scheduled: the in-flight limit was reached,
					so the offered rate was lower than configured. Raise "max requests in flight" or lower the rate.
				</p>
			{/if}

			<!-- Charts -->
			{#if showExplorer}
				<section class="rounded-lg border border-stone-200 bg-white p-5">
					<h2 class="font-semibold">Charts</h2>
					<p class="mb-3 text-xs text-stone-500">
						Pick what goes on the X axis and what each line shows; other dimensions become filters. Where a throughput line flattens, the server is saturated.
					</p>
					<ResultsExplorer cells={explorer} defaultMetrics={DEFAULT_METRICS[run.type] ?? DEFAULT_METRICS.default} />
				</section>
			{/if}

			<!-- Results table -->
			<section class="rounded-lg border border-stone-200 bg-white">
				<h2 class="px-5 pt-4 font-semibold">Results</h2>
				<p class="px-5 pb-3 text-xs text-stone-500">Latencies from successful requests only. Click a row for the full distribution.</p>
				<div class="overflow-x-auto">
					<table class="w-full text-sm">
						<thead class="bg-stone-50 text-xs text-stone-500">
							<tr class="text-right whitespace-nowrap">
								<th class="px-3 py-2 text-left font-medium">Model</th>
								{#if sweepsContext}<th class="px-3 py-2 font-medium">Context</th>{/if}
								{#if sweepsThinking}<th class="px-3 py-2 text-left font-medium">Thinking</th>{/if}
								<th class="px-3 py-2 font-medium">{openLoop ? 'Rate' : 'Users'}</th>
								<th class="px-3 py-2 text-left font-medium">Status</th>
								<th class="px-3 py-2 font-medium">Requests</th>
								<th class="px-3 py-2 font-medium">Errors</th>
								<th class="px-3 py-2 font-medium">Output tok/s</th>
								<th class="px-3 py-2 font-medium">Req/s</th>
								<th class="px-3 py-2 font-medium">TTFT p50</th>
								<th class="px-3 py-2 font-medium">TTFT p95</th>
								<th class="px-3 py-2 font-medium">E2E p50</th>
								<th class="px-3 py-2 font-medium">E2E p95</th>
								<th class="px-3 py-2 font-medium">TPOT p50</th>
								<th class="px-3 py-2 font-medium">tok/s per user</th>
								{#if goodput}
									<th class="px-3 py-2 font-medium" title="Share of completed requests meeting every SLO target">Within SLO</th>
									<th class="px-3 py-2 font-medium" title="Good requests per second">Goodput</th>
								{/if}
								{#if openLoop}<th class="px-3 py-2 font-medium" title="How late requests were sent vs. schedule (p95)">Lag p95</th>{/if}
								<th class="px-3 py-2 font-medium" title="Load generator CPU">CPU</th>
							</tr>
						</thead>
						<tbody class="divide-y divide-stone-100">
							{#each run.cells as c (c.id)}
								{@const s = c.summary}
								<tr class="cursor-pointer text-right font-mono whitespace-nowrap hover:bg-stone-50 {expanded === c.id ? 'bg-stone-50' : ''}" onclick={() => (expanded = expanded === c.id ? null : c.id)}>
									<td class="px-3 py-2 text-left font-sans">
										<span class="mr-1.5 inline-block h-2.5 w-2.5 rounded-sm align-middle" style="background: {colorOf(c)}" aria-hidden="true"></span>
										<span class="font-mono text-xs">{cellLabel(c)}</span>{#if c.profile_name}<span class="ml-1 font-mono text-xs text-stone-400">{c.model}</span>{/if}
										{#if !c.profile_name}<span class="text-xs text-stone-400">{c.source_name}</span>{/if}
									</td>
									{#if sweepsContext}<td class="px-3 py-2">{formatTokens(c.context_tokens)}</td>{/if}
									{#if sweepsThinking}<td class="px-3 py-2 text-left font-sans">{c.thinking}</td>{/if}
									<td class="px-3 py-2">{openLoop ? `${c.arrival_rate}/s` : c.concurrency}</td>
									<td class="px-3 py-2 text-left font-sans"><RunStatus status={c.status} /></td>
									<td class="px-3 py-2">{s ? int(s.requests) : '–'}</td>
									<td class="px-3 py-2 {s && s.failed ? 'text-red-700' : ''}">{s ? `${int(s.failed)} (${pct(100 * s.error_rate, 0)})` : '–'}</td>
									<td class="px-3 py-2 font-semibold">{s ? rate(s.output_throughput, '') : '–'}</td>
									<td class="px-3 py-2">{s ? s.request_throughput.toFixed(2) : '–'}</td>
									<td class="px-3 py-2">{s?.ttft_ms.count ? ms(s.ttft_ms.p50) : '–'}</td>
									<td class="px-3 py-2">{s?.ttft_ms.count ? ms(s.ttft_ms.p95) : '–'}</td>
									<td class="px-3 py-2">{s?.e2e_ms.count ? ms(s.e2e_ms.p50) : '–'}</td>
									<td class="px-3 py-2">{s?.e2e_ms.count ? ms(s.e2e_ms.p95) : '–'}</td>
									<td class="px-3 py-2">{s?.tpot_ms.count ? ms(s.tpot_ms.p50) : '–'}</td>
									<td class="px-3 py-2">{s?.output_tps.count ? rate(s.output_tps.mean, '') : '–'}</td>
									{#if goodput}
										{@const g = goodput[c.id]}
										{#if g?.requests}
											<td class="px-3 py-2 font-semibold {attainmentClass(g)}">{pct(100 * g.ratio, 0)}</td>
											<td class="px-3 py-2">{g.per_second.toFixed(2)}/s</td>
										{:else}
											<td class="px-3 py-2 text-stone-400">–</td>
											<td class="px-3 py-2 text-stone-400">–</td>
										{/if}
									{/if}
									{#if openLoop}
									<td class="px-3 py-2 {c.schedule_lag_p95_ms != null && c.schedule_lag_p95_ms >= LAG_WARN_MS ? 'text-amber-800' : 'text-stone-400'}">{ms(c.schedule_lag_p95_ms)}</td>
								{/if}
								<td class="px-3 py-2 {c.client_cpu_pct != null && c.client_cpu_pct >= CPU_WARN_PCT ? 'text-amber-800' : 'text-stone-400'}">{pct(c.client_cpu_pct, 0)}</td>
								</tr>
								{#if expanded === c.id}
									<tr class="bg-stone-50/60">
										<td colspan="19" class="px-5 py-4">
											{#if c.error}<p class="mb-3 text-sm text-red-800">⚠ {c.error}</p>{/if}
											{#if s && s.succeeded > 0}
												<table class="mb-3 w-full max-w-4xl text-right font-mono text-xs">
													<thead class="text-stone-500">
														<tr>
															<th class="py-1 text-left font-sans font-medium">Metric</th>
															{#each statCols as h (h)}<th class="px-2 py-1 font-medium">{h}</th>{/each}
														</tr>
													</thead>
													<tbody>
														{#each summaryRows as [label, key, fmt] (key)}
															{@const d = s[key] as Summary}
															<tr class="border-t border-stone-200/70">
																<td class="py-1 text-left font-sans text-stone-700">{label}</td>
																{#each statCols as h (h)}<td class="px-2 py-1">{d.count ? fmt(d[h]) : '–'}</td>{/each}
															</tr>
														{/each}
													</tbody>
												</table>
												<p class="text-xs text-stone-600">
													{int(s.succeeded)} succeeded · {int(s.failed)} failed{#if s.canceled} · {int(s.canceled)} canceled{/if}
													· {int(s.input_tokens)} input / {int(s.output_tokens)} output tokens{#if s.tokens_estimated} <span class="text-amber-700">(estimated)</span>{/if}
													· measured over {duration(s.wall_seconds)}
												</p>
											{:else if s}
												<p class="text-xs text-stone-600">No successful requests.</p>
											{/if}
											{#if goodput?.[c.id]?.requests}
												{@const g = goodput[c.id]}
												<p class="mt-1 text-xs text-stone-600">
													SLO: {int(g.good)} of {int(g.requests)} requests good{#if g.good < g.requests}; misses: {violationsText(g)}{/if}
													<span class="text-stone-400">(a request can miss several targets)</span>
												</p>
											{/if}
											{#if s && Object.keys(s.errors_by_type).length}
												<p class="mt-1 text-xs text-red-800">
													Errors: {Object.entries(s.errors_by_type).map(([k, v]) => `${k} × ${v}`).join(', ')}
												</p>
											{/if}
											<button class="mt-2 text-xs text-blue-700 underline" onclick={(e) => { e.stopPropagation(); reqCell = c.id; reqOffset = 0; document.getElementById('requests')?.scrollIntoView({ behavior: 'smooth' }); }}>
												Show this step's requests
											</button>
										</td>
									</tr>
								{/if}
							{/each}
						</tbody>
					</table>
				</div>
			</section>

			<!-- Sample outputs: the first successful response of each step -->
			{#if samples.length}
				<details class="rounded-lg border border-stone-200 bg-white" open={run.type === 'thinking_comparison'}>
					<summary class="cursor-pointer px-5 py-3 font-semibold">
						Sample outputs
						<span class="ml-2 text-xs font-normal text-stone-500">first successful response of each step, side by side</span>
					</summary>
					<div class="grid grid-cols-1 gap-3 px-5 pb-5 md:grid-cols-2 xl:grid-cols-3">
						{#each samples as c (c.id)}
							<div class="min-w-0 rounded-md border border-stone-200 p-3" style="border-top: 3px solid {colorOf(c)}">
								<div class="mb-1 truncate text-xs font-medium text-stone-700">{cellLabel(c)}</div>
								<div class="mb-2 truncate text-xs text-stone-500">{cellDims(c)}</div>
								{#if c.sample_reasoning}
									<details class="mb-2 rounded border border-stone-200">
										<summary class="cursor-pointer px-2 py-1 text-xs text-stone-500">
											Reasoning {#if c.summary?.ttfat_ms.count && c.summary.ttft_ms.count} (p50 {ms(c.summary.ttfat_ms.p50 - c.summary.ttft_ms.p50)} before the answer){/if}
										</summary>
										<div class="max-h-48 overflow-y-auto border-t border-stone-100 px-2 py-1.5">
											<Markdown source={c.sample_reasoning} class="prose-sm text-stone-600" />
										</div>
									</details>
								{/if}
								<div class="max-h-64 overflow-y-auto">
									<Markdown source={c.sample_output || '_(no answer text)_'} class="prose-sm" />
								</div>
							</div>
						{/each}
					</div>
				</details>
			{/if}

			<!-- Requests -->
			<section id="requests" class="rounded-lg border border-stone-200 bg-white">
				<div class="flex flex-wrap items-center justify-between gap-2 px-5 pt-4 pb-3">
					<h2 class="font-semibold">Requests <span class="font-normal text-stone-400">({int(reqTotal)})</span></h2>
					<div class="flex gap-2">
						<select class={selectClass} bind:value={reqCell} onchange={() => (reqOffset = 0)} aria-label="Filter by step">
							<option value="">All steps</option>
							{#each run.cells as c (c.id)}<option value={c.id}>{cellLabel(c)} · {cellDims(c)}</option>{/each}
						</select>
						<select class={selectClass} bind:value={reqStatus} onchange={() => (reqOffset = 0)} aria-label="Filter by status">
							<option value="">Any status</option>
							<option value="ok">OK</option>
							<option value="error">Error</option>
							<option value="canceled">Canceled</option>
						</select>
					</div>
				</div>
				<table class="w-full text-sm">
					<thead class="bg-stone-50 text-xs text-stone-500">
						<tr class="text-right">
							<th class="px-3 py-2 text-left font-medium">Time</th>
							<th class="px-3 py-2 text-left font-medium">Model</th>
							<th class="px-3 py-2 text-left font-medium">Step</th>
							<th class="px-3 py-2 text-left font-medium">Status</th>
							<th class="px-3 py-2 font-medium">TTFT</th>
							<th class="px-3 py-2 font-medium">E2E</th>
							<th class="px-3 py-2 font-medium">TPOT</th>
							<th class="px-3 py-2 font-medium">Tokens in → out</th>
						</tr>
					</thead>
					<tbody class="divide-y divide-stone-100">
						{#each requests as r (r.id)}
							{@const cell = cellById.get(r.cell_id)}
							<tr class="cursor-pointer text-right font-mono whitespace-nowrap hover:bg-stone-50" onclick={() => (detailId = r.id)}>
								<td class="px-3 py-1.5 text-left font-sans text-stone-600">{dateTime(r.started_at)}</td>
								<td class="px-3 py-1.5 text-left text-xs">{r.model}</td>
								<td class="px-3 py-1.5 text-left font-sans text-xs text-stone-600">{cell ? cellDims(cell) : '–'}</td>
								<td class="px-3 py-1.5 text-left font-sans {r.status === 'ok' ? 'text-green-800' : 'text-red-800'}" title={r.error_message}>
									{r.status === 'ok' ? '✓ ok' : r.status === 'canceled' ? '⏹ canceled' : `⚠ ${r.error_type || 'error'}`}
									{#if r.warmup}<span class="ml-1 rounded bg-stone-100 px-1 text-xs text-stone-500">warm-up</span>{/if}
								</td>
								<td class="px-3 py-1.5">{ms(r.metrics.ttft_ms)}</td>
								<td class="px-3 py-1.5">{ms(r.metrics.e2e_ms)}</td>
								<td class="px-3 py-1.5">{ms(r.metrics.tpot_ms)}</td>
								<td class="px-3 py-1.5">{int(r.metrics.input_tokens)} → {int(r.metrics.output_tokens)}</td>
							</tr>
						{:else}
							<tr><td colspan="8" class="px-3 py-6 text-center text-stone-400">No requests yet.</td></tr>
						{/each}
					</tbody>
				</table>
				{#if reqTotal > REQ_PAGE}
					<div class="flex items-center justify-end gap-3 px-5 py-2 text-xs text-stone-600">
						<span>{reqOffset + 1}–{Math.min(reqOffset + REQ_PAGE, reqTotal)} of {int(reqTotal)}</span>
						<button class="rounded px-2 py-1 hover:bg-stone-100 disabled:opacity-40" disabled={reqOffset === 0} onclick={() => (reqOffset = Math.max(0, reqOffset - REQ_PAGE))}>← Newer</button>
						<button class="rounded px-2 py-1 hover:bg-stone-100 disabled:opacity-40" disabled={reqOffset + REQ_PAGE >= reqTotal} onclick={() => (reqOffset += REQ_PAGE)}>Older →</button>
					</div>
				{/if}
			</section>
		{/if}
	</div>
</div>

{#if detailId}
	<RequestDetail requestId={detailId} onclose={() => (detailId = null)} />
{/if}
