<script lang="ts">
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';
	import {
		ApiError,
		definitionVersions,
		deleteComparison,
		deleteDefinition,
		exportDefinitionUrl,
		exportRunsUrl,
		importDefinition,
		importRuns,
		listComparisons,
		listDefinitions,
		listRuns,
		runDefinition
	} from '$lib/api';
	import { loadSummary, typeLabel } from '$lib/dims';
	import { downloadUrl, isRunBundle } from '$lib/download';
	import { dateTime, rate, relativeTime } from '$lib/format';
	import type { Comparison, Definition, DefinitionVersion, Run } from '$lib/types';
	import RunStatus from '$lib/components/RunStatus.svelte';

	const MAX_COMPARE = 8;

	let runs = $state<Run[]>([]);
	let comparisons = $state<Comparison[]>([]);
	let definitions = $state<Definition[]>([]);
	let versions = $state<Record<string, DefinitionVersion[]>>({});
	let openVersions = $state('');
	let selected = $state<string[]>([]);
	let loading = $state(true);
	let error = $state('');
	let notice = $state('');
	let busy = $state('');
	let fileInput = $state<HTMLInputElement>();

	let refresh = () => {};

	onMount(() => {
		let timer: ReturnType<typeof setTimeout>;
		let alive = true;
		async function load() {
			clearTimeout(timer);
			try {
				runs = await listRuns();
				error = '';
			} catch (e) {
				error = (e as Error).message;
			} finally {
				loading = false;
			}
			// Refresh while something is running.
			if (alive && runs.some((r) => r.status === 'running')) timer = setTimeout(load, 2000);
		}
		refresh = load;
		load();
		listComparisons()
			.then((c) => (comparisons = c))
			.catch(() => {});
		loadDefinitions();
		return () => {
			alive = false;
			clearTimeout(timer);
		};
	});

	async function loadDefinitions() {
		try {
			definitions = await listDefinitions();
		} catch (e) {
			error = (e as Error).message;
		}
	}

	function toggle(id: string) {
		selected = selected.includes(id) ? selected.filter((x) => x !== id) : [...selected, id];
	}

	// Keep the list order (newest first) reversed so the older run is the baseline.
	function selectedInOrder(): string[] {
		const order = runs.map((r) => r.id).reverse();
		return [...selected].sort((a, b) => order.indexOf(a) - order.indexOf(b));
	}

	function compare() {
		goto(`/benchmarks/compare?runs=${selectedInOrder().join(',')}`);
	}

	const selectedActive = $derived(
		runs.some((r) => selected.includes(r.id) && (r.status === 'running' || r.status === 'pending'))
	);

	async function removeComparison(c: Comparison) {
		if (!confirm(`Delete saved comparison "${c.name}"? The runs are kept.`)) return;
		try {
			await deleteComparison(c.id);
			comparisons = comparisons.filter((x) => x.id !== c.id);
		} catch (e) {
			error = (e as Error).message;
		}
	}

	async function run(d: Definition) {
		busy = d.id;
		error = '';
		try {
			const r = await runDefinition(d.id);
			goto(`/benchmarks/${r.id}`);
		} catch (e) {
			error = (e as Error).message;
			if (e instanceof ApiError && e.status === 409) error += ' Wait for it to finish or stop it.';
		} finally {
			busy = '';
		}
	}

	async function removeDefinition(d: Definition) {
		if (!confirm(`Delete saved definition "${d.name}" and all its versions? Runs started from it are kept.`)) return;
		try {
			await deleteDefinition(d.id);
			definitions = definitions.filter((x) => x.id !== d.id);
		} catch (e) {
			error = (e as Error).message;
		}
	}

	async function toggleVersions(d: Definition) {
		if (openVersions === d.id) {
			openVersions = '';
			return;
		}
		openVersions = d.id;
		try {
			versions[d.id] = await definitionVersions(d.id);
		} catch (e) {
			error = (e as Error).message;
		}
	}

	/** Imports a run bundle (.llmbench.json) or a definition file (YAML/JSON). */
	async function importFile(e: Event & { currentTarget: HTMLInputElement }) {
		const file = e.currentTarget.files?.[0];
		e.currentTarget.value = '';
		if (!file) return;
		error = '';
		notice = '';
		busy = 'import';
		try {
			if (await isRunBundle(file)) {
				const results = await importRuns(file);
				const imported = results.filter((r) => r.status === 'imported');
				const skipped = results.filter((r) => r.status === 'skipped');
				notice = [
					imported.length
						? `Imported ${imported.length} run${imported.length === 1 ? '' : 's'}: ${imported.map((r) => r.name).join(', ')}.`
						: '',
					skipped.length ? `Skipped ${skipped.length} already present: ${skipped.map((r) => r.name).join(', ')}.` : ''
				]
					.filter(Boolean)
					.join(' ');
				refresh();
			} else {
				const d = await importDefinition(file);
				notice = `Imported definition “${d.name}”${d.version > 1 ? ` as version ${d.version} (a definition with that name existed)` : ''}.`;
				await loadDefinitions();
			}
		} catch (err) {
			error = `Import failed: ${(err as Error).message}`;
		} finally {
			busy = '';
		}
	}

	function peakThroughput(r: Run): number | null {
		const vals = r.cells.map((c) => c.summary?.output_throughput ?? 0).filter((v) => v > 0);
		return vals.length ? Math.max(...vals) : null;
	}

	function models(r: Run): string {
		return [...new Set(r.cells.map((c) => c.profile_name || c.model))].join(', ');
	}

	function dimsSummary(r: Pick<Run, 'config'>): string {
		const parts = [loadSummary(r)];
		if (r.config.context_lengths?.length) parts.push(`${r.config.context_lengths.length} context lengths`);
		if (r.config.thinking_levels?.length) parts.push(`thinking ${r.config.thinking_levels.join('/')}`);
		return parts.join(' · ');
	}

	function targetsSummary(d: Pick<Definition, 'config'>): string {
		const targets = d.config.targets ?? [];
		const named = targets.filter((t) => t.model).map((t) => t.model);
		if (named.length === targets.length) return named.join(', ');
		return `${targets.length} model${targets.length === 1 ? '' : 's'}`;
	}

	const menuItem = 'block w-full px-3 py-1.5 text-left hover:bg-stone-100';
</script>

<div class="mx-auto h-full max-w-6xl overflow-y-auto p-6">
	<div class="mb-5 flex flex-wrap items-center justify-between gap-3">
		<div>
			<h1 class="text-xl font-semibold">Benchmarks</h1>
			<p class="text-sm text-stone-500">Load tests with simulated users or fixed request rates. Every request is measured and stored.</p>
		</div>
		<div class="flex gap-2">
			<input bind:this={fileInput} type="file" class="hidden" accept=".json,.yaml,.yml" onchange={importFile} />
			<button
				class="rounded-md border border-stone-300 bg-white px-3 py-1.5 text-sm text-stone-700 hover:bg-stone-50 disabled:opacity-50"
				title="Import exported runs (.llmbench.json) or a test definition (YAML/JSON)"
				disabled={busy === 'import'}
				onclick={() => fileInput?.click()}
			>
				{busy === 'import' ? 'Importing…' : 'Import…'}
			</button>
			<a href="/benchmarks/new" class="rounded-md bg-blue-600 px-4 py-1.5 text-sm font-medium text-white hover:bg-blue-700">
				+ New benchmark
			</a>
		</div>
	</div>

	{#if error}
		<p class="mb-4 rounded bg-red-50 px-3 py-2 text-sm text-red-800">⚠ {error}</p>
	{/if}
	{#if notice}
		<p class="mb-4 flex items-start justify-between gap-3 rounded bg-green-50 px-3 py-2 text-sm text-green-800">
			<span>✓ {notice}</span>
			<button class="text-green-700 hover:text-green-900" aria-label="Dismiss" onclick={() => (notice = '')}>✕</button>
		</p>
	{/if}

	<section class="mb-6">
		<div class="mb-2 flex flex-wrap items-baseline justify-between gap-2">
			<h2 class="text-sm font-semibold text-stone-700">Saved definitions</h2>
			{#if definitions.length === 0}
				<span class="text-xs text-stone-400">Use “Save as definition” in the builder to keep a test and re-run it with one click.</span>
			{/if}
		</div>
		{#if definitions.length}
			<ul class="divide-y divide-stone-100 rounded-lg border border-stone-200 bg-white text-sm">
				{#each definitions as d (d.id)}
					<li class="px-4 py-2.5">
						<div class="flex flex-wrap items-center gap-x-4 gap-y-1">
							<div class="min-w-0 flex-1">
								<a href="/benchmarks/new?def={d.id}" class="font-medium text-stone-900 hover:text-blue-700 hover:underline">{d.name}</a>
								<button
									class="ml-1 rounded bg-stone-100 px-1.5 text-xs text-stone-600 hover:bg-stone-200"
									title="Show versions"
									aria-expanded={openVersions === d.id}
									onclick={() => toggleVersions(d)}
								>
									v{d.version}
								</button>
								<span class="ml-2 text-xs text-stone-500">
									{typeLabel({ type: d.config.type, config: d.config })} · <span class="font-mono">{targetsSummary(d)}</span> · {dimsSummary(d)}
								</span>
								{#if d.config.slo}
									<span class="ml-1 rounded bg-emerald-50 px-1.5 text-xs text-emerald-800" title="Has goodput targets">SLO</span>
								{/if}
								{#if d.description}<p class="truncate text-xs text-stone-500" title={d.description}>{d.description}</p>{/if}
							</div>
							<span class="text-xs text-stone-400">updated {relativeTime(d.updated_at)}</span>
							<div class="flex items-center gap-1">
								<button
									class="rounded-md bg-blue-600 px-3 py-1 text-xs font-medium text-white hover:bg-blue-700 disabled:opacity-50"
									disabled={busy === d.id}
									onclick={() => run(d)}
								>
									{busy === d.id ? 'Starting…' : 'Run'}
								</button>
								<a href="/benchmarks/new?def={d.id}" class="rounded-md px-2 py-1 text-xs text-stone-600 hover:bg-stone-100">Edit</a>
								<details class="relative">
									<summary class="cursor-pointer list-none rounded-md px-2 py-1 text-xs text-stone-600 hover:bg-stone-100">Export ▾</summary>
									<div class="absolute right-0 z-20 mt-1 w-32 rounded-md border border-stone-200 bg-white py-1 text-xs shadow-lg">
										<button class={menuItem} onclick={() => downloadUrl(exportDefinitionUrl(d.id, 'yaml'))}>YAML</button>
										<button class={menuItem} onclick={() => downloadUrl(exportDefinitionUrl(d.id, 'json'))}>JSON</button>
									</div>
								</details>
								<button
									class="rounded-md px-2 py-1 text-xs text-stone-400 hover:bg-stone-100 hover:text-red-700"
									aria-label="Delete definition"
									onclick={() => removeDefinition(d)}>✕</button
								>
							</div>
						</div>
						{#if openVersions === d.id}
							<ul class="mt-2 space-y-1 border-l-2 border-stone-200 pl-3 text-xs">
								{#each versions[d.id] ?? [] as v (v.version)}
									<li class="flex flex-wrap items-center gap-x-3">
										<span class="w-8 font-medium">v{v.version}</span>
										<span class="text-stone-500">{dateTime(v.created_at)}</span>
										<span class="text-stone-500">
											{typeLabel({ type: v.config.type, config: v.config })} · <span class="font-mono">{targetsSummary(v)}</span> · {dimsSummary(v)}
										</span>
										{#if v.version === d.version}
											<span class="text-stone-400">latest</span>
										{:else}
											<a
												href="/benchmarks/new?def={d.id}&version={v.version}"
												class="text-blue-700 hover:underline"
												title="Open this version in the builder; saving makes it the latest version">Restore…</a
											>
										{/if}
									</li>
								{:else}
									<li class="text-stone-400">Loading…</li>
								{/each}
							</ul>
						{/if}
					</li>
				{/each}
			</ul>
		{/if}
	</section>

	{#if comparisons.length}
		<section class="mb-6">
			<h2 class="mb-2 text-sm font-semibold text-stone-700">Saved comparisons</h2>
			<ul class="flex flex-wrap gap-2">
				{#each comparisons as c (c.id)}
					<li class="group flex items-center gap-1 rounded-md border border-stone-200 bg-white py-1 pr-1 pl-3 text-sm">
						<a href="/benchmarks/compare?c={c.id}" class="hover:text-blue-700 hover:underline">{c.name}</a>
						<span class="text-xs text-stone-400">{c.run_ids.length} runs · {relativeTime(c.updated_at)}</span>
						<button class="rounded px-1.5 text-stone-400 hover:bg-stone-100 hover:text-stone-700" aria-label="Delete comparison" onclick={() => removeComparison(c)}>✕</button>
					</li>
				{/each}
			</ul>
		</section>
	{/if}

	{#if loading}
		<p class="text-sm text-stone-500">Loading…</p>
	{:else if runs.length === 0}
		<div class="rounded-md border border-dashed border-stone-300 p-8 text-center text-sm text-stone-600">
			No benchmarks yet.
			<a href="/benchmarks/new" class="text-blue-700 underline">Create your first one</a>: pick a model, choose the load, and run it.
		</div>
	{:else}
		<!-- Selection action bar: always visible so the checkboxes' purpose is clear. -->
		<div class="sticky top-0 z-10 mb-2 flex flex-wrap items-center gap-3 rounded-lg border px-4 py-2 text-sm {selected.length ? 'border-blue-300 bg-blue-50' : 'border-stone-200 bg-white'}">
			<span class="text-stone-700">
				{#if selected.length === 0}
					Tick runs to compare them side by side (2 to {MAX_COMPARE}) or to export them.
				{:else if selected.length === 1}
					<b>1</b> run selected. Tick at least one more to compare.
				{:else}
					<b>{selected.length}</b> runs selected{selected.length >= MAX_COMPARE ? ' (maximum)' : ''}.
				{/if}
			</span>
			<div class="ml-auto flex gap-2">
				{#if selected.length}
					<button class="rounded-md px-3 py-1 text-stone-600 hover:bg-white" onclick={() => (selected = [])}>Clear</button>
					<button
						class="rounded-md border border-stone-300 bg-white px-3 py-1 text-stone-700 hover:bg-stone-50 disabled:cursor-not-allowed disabled:opacity-40"
						title={selectedActive ? 'Wait for running benchmarks to finish' : 'Download a JSON file that another LLMBench can import'}
						disabled={selectedActive}
						onclick={() => downloadUrl(exportRunsUrl(selectedInOrder()))}
					>
						Export JSON
					</button>
				{/if}
				<button
					class="rounded-md bg-blue-600 px-4 py-1 font-medium text-white hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-40"
					disabled={selected.length < 2}
					onclick={compare}
				>
					Compare{selected.length >= 2 ? ` ${selected.length} runs` : ''}
				</button>
			</div>
		</div>
		<div class="overflow-hidden rounded-lg border border-stone-200 bg-white">
			<table class="w-full text-sm">
				<thead class="bg-stone-50 text-xs text-stone-500">
					<tr class="text-left">
						<th class="w-8 px-3 py-2"><span class="sr-only">Select</span></th>
						<th class="px-3 py-2 font-medium">Name</th>
						<th class="px-3 py-2 font-medium">Type</th>
						<th class="px-3 py-2 font-medium">Models</th>
						<th class="px-3 py-2 font-medium">Dimensions</th>
						<th class="px-3 py-2 font-medium">Status</th>
						<th class="px-3 py-2 text-right font-medium">Peak throughput</th>
						<th class="px-3 py-2 font-medium">Started</th>
					</tr>
				</thead>
				<tbody class="divide-y divide-stone-100">
					{#each runs as r (r.id)}
						<tr class="hover:bg-stone-50 {selected.includes(r.id) ? 'bg-blue-50/40' : ''}">
							<td class="px-3 py-2.5">
								<input
									type="checkbox"
									aria-label="Select {r.name}"
									checked={selected.includes(r.id)}
									disabled={!selected.includes(r.id) && selected.length >= MAX_COMPARE}
									onchange={() => toggle(r.id)}
								/>
							</td>
							<td class="px-3 py-2.5">
								<a href="/benchmarks/{r.id}" class="font-medium text-stone-900 hover:text-blue-700 hover:underline">{r.name}</a>
								{#if r.imported_at}
									<span class="ml-1 rounded bg-violet-50 px-1.5 text-xs text-violet-800" title="Imported {dateTime(r.imported_at)}">imported</span>
								{/if}
							</td>
							<td class="px-3 py-2.5 whitespace-nowrap text-stone-600">{typeLabel(r)}</td>
							<td class="max-w-56 truncate px-3 py-2.5 font-mono text-xs" title={models(r)}>{models(r)}</td>
							<td class="max-w-56 truncate px-3 py-2.5 text-xs text-stone-600" title={dimsSummary(r)}>{dimsSummary(r)}</td>
							<td class="px-3 py-2.5 whitespace-nowrap">
								<RunStatus status={r.status} />
								{#if r.progress}
									<span class="ml-1 text-xs text-stone-500">{r.progress.cell_index + 1}/{r.progress.cell_count}</span>
								{/if}
							</td>
							<td class="px-3 py-2.5 text-right font-mono whitespace-nowrap">{rate(peakThroughput(r))}</td>
							<td class="px-3 py-2.5 whitespace-nowrap text-stone-600">{dateTime(r.created_at)}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</div>
