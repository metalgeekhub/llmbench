<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { createComparison, exportRunsUrl, getComparison, getRun, getRunGoodput, updateComparison } from '$lib/api';
	import { explorerCells, loadSummary, sloSummary, typeLabel, type ExplorerCell } from '$lib/dims';
	import { downloadText, downloadUrl, safeFilename } from '$lib/download';
	import { dateTime } from '$lib/format';
	import { winnerTiles } from '$lib/insights';
	import { seriesColor } from '$lib/palette';
	import { comparisonReport } from '$lib/report';
	import type { Comparison, Goodput, Run } from '$lib/types';
	import ResultsExplorer, { type ExplorerSettings } from '$lib/components/ResultsExplorer.svelte';
	import RunStatus from '$lib/components/RunStatus.svelte';

	const MAX_SERIES = 8;

	let runs = $state<Run[]>([]);
	let goodput = $state<Record<string, Record<string, Goodput> | null>>({});
	let saved = $state<Comparison | null>(null);
	let settings = $state<ExplorerSettings>({});
	let loading = $state(true);
	let error = $state('');
	let notice = $state('');
	/** Bumped to remount the explorer when a saved comparison's settings load. */
	let explorerKey = $state(0);

	$effect(() => {
		const savedId = page.url.searchParams.get('c');
		const ids = (page.url.searchParams.get('runs') ?? '').split(',').filter(Boolean);
		load(savedId, ids);
	});

	async function load(savedId: string | null, ids: string[]) {
		loading = true;
		error = '';
		try {
			let runIds = ids;
			if (savedId) {
				saved = await getComparison(savedId);
				runIds = saved.run_ids;
				settings = (saved.settings as ExplorerSettings) ?? {};
			} else {
				saved = null;
			}
			if (runIds.length < 2) {
				error = 'Pick at least two runs on the Benchmarks page to compare.';
				runs = [];
				return;
			}
			const results = await Promise.allSettled(runIds.map((id) => getRun(id)));
			const loaded = results.flatMap((r) => (r.status === 'fulfilled' ? [r.value] : []));
			// Goodput under each run's own saved SLO.
			const gp = await Promise.all(
				loaded.map((r) => (r.config.slo ? getRunGoodput(r.id).then((g) => g.cells).catch(() => null) : null))
			);
			goodput = Object.fromEntries(loaded.map((r, i) => [r.id, gp[i]]));
			runs = loaded;
			const missing = results.length - runs.length;
			if (missing) notice = `${missing} run${missing === 1 ? ' was' : 's were'} deleted and left out.`;
			explorerKey++;
		} catch (e) {
			error = (e as Error).message;
		} finally {
			loading = false;
		}
	}

	// Each (run, model) pair is one series; colors follow that order.
	const allCells = $derived.by((): ExplorerCell[] => {
		let offset = 0;
		const out: ExplorerCell[] = [];
		for (const r of runs) {
			out.push(...explorerCells(r, { entityPrefix: r.name, entityOffset: offset, goodput: goodput[r.id] }));
			offset += r.config.targets.length;
		}
		return out;
	});
	const entityCount = $derived(new Set(allCells.map((c) => c.entity)).size);
	// The palette has 8 distinguishable colors: beyond that, keep the first 8 series.
	const cells = $derived(allCells.filter((c) => c.entityIndex < MAX_SERIES));

	const winners = $derived(winnerTiles(cells));
	const sloRuns = $derived(runs.filter((r) => r.config.slo));
	const title = $derived(saved?.name ?? runs.map((r) => r.name).join(' vs '));

	const menuItem = 'block w-full px-3 py-1.5 text-left hover:bg-stone-100';
	const menuHint = 'block text-xs text-stone-400';

	function exportReport(format: 'html' | 'md') {
		const text = comparisonReport(format, { title, runs, cells, settings });
		downloadText(`${safeFilename(title)}.${format}`, text, format === 'html' ? 'text/html' : 'text/markdown');
	}

	async function save() {
		const name = prompt('Name this comparison', saved?.name ?? runs.map((r) => r.name).join(' vs ').slice(0, 100));
		if (!name?.trim()) return;
		const input = { name: name.trim(), run_ids: runs.map((r) => r.id), settings: $state.snapshot(settings) as Record<string, unknown> };
		try {
			if (saved) {
				saved = await updateComparison(saved.id, input);
				notice = 'Saved.';
			} else {
				saved = await createComparison(input);
				goto(`/benchmarks/compare?c=${saved.id}`, { replaceState: true, keepFocus: true, noScroll: true });
				notice = 'Saved. Find it on the Benchmarks page.';
			}
		} catch (e) {
			error = (e as Error).message;
		}
	}
</script>

<div class="h-full overflow-y-auto">
	<div class="mx-auto max-w-7xl space-y-6 p-6">
		<a href="/benchmarks" class="text-xs text-stone-500 hover:text-stone-800">← Benchmarks</a>
		<div class="flex flex-wrap items-start justify-between gap-3">
			<div>
				<h1 class="text-xl font-semibold">{saved?.name ?? 'Compare runs'}</h1>
				<p class="text-sm text-stone-500">
					Each line is one model in one run. Tables show every value, the best per row (★) and the difference from the first line.
				</p>
			</div>
			{#if runs.length >= 2}
				<div class="flex gap-2 text-sm">
					<details class="relative">
						<summary class="cursor-pointer list-none rounded-md border border-stone-300 bg-white px-3 py-1.5 hover:bg-stone-50">Export ▾</summary>
						<div class="absolute right-0 z-20 mt-1 w-60 rounded-md border border-stone-200 bg-white py-1 text-sm shadow-lg">
							<button class={menuItem} onclick={() => exportReport('html')}>HTML report <span class={menuHint}>charts and tables, for sharing</span></button>
							<button class={menuItem} onclick={() => exportReport('md')}>Markdown summary <span class={menuHint}>for issues, wikis, PRs</span></button>
							<button class={menuItem} onclick={() => downloadUrl(exportRunsUrl(runs.map((r) => r.id)))}>
								JSON (all runs) <span class={menuHint}>re-importable into LLMBench</span>
							</button>
						</div>
					</details>
					<button class="rounded-md border border-stone-300 bg-white px-4 py-1.5 font-medium hover:bg-stone-50" onclick={save}>
						{saved ? 'Save changes' : 'Save comparison'}
					</button>
				</div>
			{/if}
		</div>

		{#if error}
			<p class="rounded bg-red-50 px-3 py-2 text-sm text-red-800">⚠ {error}</p>
		{/if}
		{#if notice}
			<p class="rounded bg-stone-100 px-3 py-2 text-sm text-stone-700">{notice}</p>
		{/if}

		{#if loading}
			<p class="text-sm text-stone-500">Loading…</p>
		{:else if runs.length >= 2}
			<section class="overflow-x-auto rounded-lg border border-stone-200 bg-white">
				<table class="w-full text-sm">
					<thead class="bg-stone-50 text-xs text-stone-500">
						<tr class="text-left">
							<th class="px-4 py-2 font-medium">Run</th>
							<th class="px-4 py-2 font-medium">Type</th>
							<th class="px-4 py-2 font-medium">Models</th>
							<th class="px-4 py-2 font-medium">Load</th>
							<th class="px-4 py-2 font-medium">Status</th>
							<th class="px-4 py-2 font-medium">Started</th>
						</tr>
					</thead>
					<tbody class="divide-y divide-stone-100">
						{#each runs as r, ri (r.id)}
							{@const offset = runs.slice(0, ri).reduce((n, x) => n + x.config.targets.length, 0)}
							<tr>
								<td class="px-4 py-2">
									<a href="/benchmarks/{r.id}" class="font-medium hover:text-blue-700 hover:underline">{r.name}</a>
									{#if r.config.slo}<div class="text-xs text-emerald-800" title="Goodput targets">SLO: {sloSummary(r.config.slo)}</div>{/if}
								</td>
								<td class="px-4 py-2 text-stone-600">{typeLabel(r)}</td>
								<td class="px-4 py-2 text-xs">
									{#each [...new Map(r.cells.map((c) => [c.profile_name || c.model, c])).keys()] as label, ti (label)}
										<span class="mr-2 whitespace-nowrap">
											<span class="mr-1 inline-block h-2 w-2 rounded-sm align-middle" style="background: {seriesColor(offset + ti)}" aria-hidden="true"></span><span class="font-mono">{label}</span>
										</span>
									{/each}
								</td>
								<td class="px-4 py-2 text-xs text-stone-600">{loadSummary(r)}</td>
								<td class="px-4 py-2"><RunStatus status={r.status} /></td>
								<td class="px-4 py-2 text-xs whitespace-nowrap text-stone-600">{dateTime(r.created_at)}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</section>

			{#if entityCount > MAX_SERIES}
				<p class="rounded bg-amber-50 px-3 py-2 text-sm text-amber-900">
					⚠ {entityCount} model series in total; only the first {MAX_SERIES} are shown so every line keeps a distinct color.
				</p>
			{/if}

			{#if winners.length}
				<section class="grid grid-cols-1 gap-3 sm:grid-cols-2 {winners.length > 4 ? 'xl:grid-cols-5' : 'xl:grid-cols-4'}" aria-label="Winners">
					{#each winners as w (w.label)}
						<div class="min-w-0 rounded-lg border border-stone-200 bg-white px-4 py-3">
							<div class="truncate text-xs text-stone-500">{w.label}</div>
							<div class="mt-0.5 text-2xl font-semibold tracking-tight">{w.value}</div>
							{#if w.who}
								<div class="mt-0.5 truncate text-sm text-stone-800" title={w.who}><span class="text-green-800">★</span> {w.who}</div>
							{:else}
								<div class="mt-0.5 text-sm text-stone-500">= Tie</div>
							{/if}
							{#if w.margin}<div class="truncate text-xs text-stone-500" title={w.margin}>{w.margin}</div>{/if}
						</div>
					{/each}
				</section>
				<p class="-mt-3 text-[11px] text-stone-400">
					Each line's best step, whatever load it was measured at.
					{#if sloRuns.length}
						Goodput uses each run's own SLO{sloRuns.length < runs.length ? `; ${runs.length - sloRuns.length} run${runs.length - sloRuns.length === 1 ? ' has' : 's have'} none` : ''}.
					{/if}
				</p>
			{/if}

			<section class="rounded-lg border border-stone-200 bg-white p-5">
				{#key explorerKey}
					<ResultsExplorer {cells} bind:settings showDelta entityNoun="Run · model" />
				{/key}
			</section>
		{/if}
	</div>
</div>
