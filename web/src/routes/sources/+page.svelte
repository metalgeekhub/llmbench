<script lang="ts">
	import { onMount } from 'svelte';
	import { deleteSource, discoverModels, listSources } from '$lib/api';
	import type { Source } from '$lib/types';
	import SourceForm from '$lib/components/SourceForm.svelte';

	let sources = $state<Source[]>([]);
	let loading = $state(true);
	let error = $state('');
	/** 'new', a source ID being edited, or null. */
	let editing = $state<string | null>(null);
	let checks = $state<Record<string, { models?: string[]; error?: string } | 'loading'>>({});

	const editingSource = $derived(sources.find((s) => s.id === editing));

	onMount(load);

	async function load() {
		try {
			sources = await listSources();
		} catch (e) {
			error = (e as Error).message;
		} finally {
			loading = false;
		}
	}

	async function remove(s: Source) {
		if (!confirm(`Delete source "${s.name}"? Its request history is kept.`)) return;
		try {
			await deleteSource(s.id);
			await load();
		} catch (e) {
			error = (e as Error).message;
		}
	}

	async function check(s: Source) {
		checks[s.id] = 'loading';
		try {
			checks[s.id] = { models: await discoverModels(s.id) };
		} catch (e) {
			checks[s.id] = { error: (e as Error).message };
		}
	}

	async function saved() {
		editing = null;
		await load();
	}
</script>

<div class="mx-auto max-w-4xl overflow-y-auto p-6" style="max-height: 100%">
	<div class="mb-5 flex items-center justify-between">
		<div>
			<h1 class="text-xl font-semibold">Sources</h1>
			<p class="text-sm text-stone-500">LLM endpoints you can chat with and benchmark.</p>
		</div>
		{#if editing === null}
			<button class="rounded-md bg-blue-600 px-4 py-1.5 text-sm font-medium text-white hover:bg-blue-700" onclick={() => (editing = 'new')}>
				+ Add source
			</button>
		{/if}
	</div>

	{#if error}
		<p class="mb-4 rounded bg-red-50 px-3 py-2 text-sm text-red-800">⚠ {error}</p>
	{/if}

	{#if editing === 'new'}
		<div class="mb-6 rounded-lg border border-stone-200 bg-white p-5">
			<h2 class="mb-4 font-semibold">New source</h2>
			<SourceForm onsaved={saved} oncancel={() => (editing = null)} />
		</div>
	{/if}

	{#if loading}
		<p class="text-sm text-stone-500">Loading…</p>
	{:else if sources.length === 0 && editing === null}
		<div class="rounded-md border border-dashed border-stone-300 p-6 text-sm text-stone-600">
			No sources yet. Add one here, or set environment variables such as
			<code class="rounded bg-stone-100 px-1">LLMB_SOURCE_LOCAL_TYPE=openai</code> and
			<code class="rounded bg-stone-100 px-1">LLMB_SOURCE_LOCAL_BASE_URL=http://localhost:8000/v1</code>.
		</div>
	{/if}

	<ul class="space-y-3">
		{#each sources as s (s.id)}
			<li class="rounded-lg border border-stone-200 bg-white p-4">
				{#if editing === s.id && editingSource}
					<h2 class="mb-4 font-semibold">Edit {s.name}</h2>
					{#key s.id}
						<SourceForm source={editingSource} onsaved={saved} oncancel={() => (editing = null)} />
					{/key}
				{:else}
					<div class="flex flex-wrap items-start justify-between gap-3">
						<div class="min-w-0">
							<div class="flex items-center gap-2">
								<h2 class="font-semibold">{s.name}</h2>
								<span class="rounded bg-stone-100 px-1.5 py-0.5 text-xs text-stone-600">{s.type}</span>
								{#if s.read_only}
									<span class="rounded bg-amber-50 px-1.5 py-0.5 text-xs text-amber-800" title="Defined by LLMB_SOURCE_* environment variables or the config file">
										🔒 managed by environment
									</span>
								{/if}
							</div>
							<p class="mt-0.5 truncate font-mono text-xs text-stone-500">{s.base_url}</p>
						</div>
						<div class="flex gap-1 text-sm">
							<button class="rounded px-2.5 py-1 text-stone-600 hover:bg-stone-100 disabled:opacity-50" disabled={checks[s.id] === 'loading'} onclick={() => check(s)}>
								{checks[s.id] === 'loading' ? 'Checking…' : 'Check models'}
							</button>
							{#if !s.read_only}
								<button class="rounded px-2.5 py-1 text-stone-600 hover:bg-stone-100" onclick={() => (editing = s.id)}>Edit</button>
								<button class="rounded px-2.5 py-1 text-red-700 hover:bg-red-50" onclick={() => remove(s)}>Delete</button>
							{/if}
						</div>
					</div>
					<dl class="mt-3 grid grid-cols-2 gap-x-6 gap-y-1 text-xs sm:grid-cols-4">
						<div>
							<dt class="text-stone-400">Models</dt>
							<dd class="text-stone-700">{s.models_auto ? 'auto (/models)' : `${s.models.length} configured`}</dd>
						</div>
						<div>
							<dt class="text-stone-400">API key</dt>
							<dd class="text-stone-700">{s.has_api_key ? 'set' : 'none'}</dd>
						</div>
						<div>
							<dt class="text-stone-400">Headers</dt>
							<dd class="truncate text-stone-700">{s.header_names.length ? s.header_names.join(', ') : 'none'}</dd>
						</div>
						<div>
							<dt class="text-stone-400">Timeout</dt>
							<dd class="text-stone-700">{s.timeout_seconds ? `${s.timeout_seconds}s` : 'default'}{s.tls_skip_verify ? ' · TLS unverified' : ''}</dd>
						</div>
					</dl>
					{#if !s.models_auto && s.models.length}
						<p class="mt-2 font-mono text-xs text-stone-600">{s.models.join(', ')}</p>
					{/if}
					{#if s.extra_body && Object.keys(s.extra_body).length}
						<p class="mt-2 truncate font-mono text-xs text-stone-500" title={JSON.stringify(s.extra_body)}>
							extra body: {JSON.stringify(s.extra_body)}
						</p>
					{/if}
					{@const c = checks[s.id]}
					{#if c && c !== 'loading'}
						<div class="mt-3 rounded bg-stone-50 px-3 py-2 text-xs">
							{#if c.error}
								<span class="text-red-800">⚠ {c.error}</span>
							{:else if c.models}
								<span class="text-green-800">✓ {c.models.length} model{c.models.length === 1 ? '' : 's'} available:</span>
								<span class="font-mono text-stone-700">{c.models.join(', ') || '(none)'}</span>
							{/if}
						</div>
					{/if}
				{/if}
			</li>
		{/each}
	</ul>
</div>
