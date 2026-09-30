<script lang="ts">
	import { untrack } from 'svelte';
	import { createProfile, sourceModels, updateProfile } from '$lib/api';
	import { emptyParams, normalizeParams } from '$lib/params';
	import type { ChatParams, Profile, Source } from '$lib/types';
	import ParamsEditor from './ParamsEditor.svelte';

	let {
		profile,
		sources,
		onsaved,
		oncancel
	}: { profile?: Profile; sources: Source[]; onsaved: (p: Profile) => void; oncancel: () => void } = $props();

	// Initial values come from `profile` once; the parent remounts per profile.
	const initial = (() => profile)();

	let name = $state(initial?.name ?? '');
	let sourceId = $state(initial?.source_id ?? untrack(() => sources[0]?.id) ?? '');
	let model = $state(initial?.model ?? '');
	let params = $state<ChatParams>(initial ? normalizeParams(initial.params) : emptyParams());
	let paramsError = $state('');
	let models = $state<string[]>([]);
	let saving = $state(false);
	let error = $state('');

	const source = $derived(sources.find((s) => s.id === sourceId));

	$effect(() => {
		const id = sourceId;
		if (!id) return;
		sourceModels(id)
			.then((ml) => {
				if (id !== sourceId) return;
				models = ml.models;
				if (!model && ml.models.length) model = ml.models[0];
			})
			.catch(() => (models = []));
	});

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (paramsError) {
			error = paramsError;
			return;
		}
		saving = true;
		error = '';
		const input = { name, source_id: sourceId, model, params: $state.snapshot(params) };
		try {
			onsaved(initial ? await updateProfile(initial.id, input) : await createProfile(input));
		} catch (err) {
			error = (err as Error).message;
		} finally {
			saving = false;
		}
	}

	const inputClass = 'w-full rounded-md border border-stone-300 bg-white px-2 py-1.5 text-sm';
	const label = 'mb-1 block text-xs font-medium text-stone-600';
</script>

<form class="space-y-4 text-sm" onsubmit={submit}>
	<label class="block">
		<span class={label}>Name</span>
		<input class={inputClass} required maxlength="64" placeholder="Qwen3 thinking-high" bind:value={name} />
	</label>
	<div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
		<label class="block">
			<span class={label}>Source</span>
			<select class={inputClass} bind:value={sourceId} onchange={() => (model = '')}>
				{#each sources as s (s.id)}<option value={s.id}>{s.name}</option>{/each}
			</select>
		</label>
		<label class="block">
			<span class={label}>Model</span>
			<input class="{inputClass} font-mono" list="profile-models" required bind:value={model} />
			<datalist id="profile-models">
				{#each models as m (m)}<option value={m}></option>{/each}
			</datalist>
		</label>
	</div>
	<div class="rounded-md border border-stone-200 p-3">
		<ParamsEditor bind:params bind:error={paramsError} baseUrl={source?.base_url} />
	</div>
	{#if error}<p class="rounded bg-red-50 px-3 py-2 text-red-800">⚠ {error}</p>{/if}
	<div class="flex gap-2">
		<button type="submit" class="rounded-md bg-blue-600 px-4 py-1.5 font-medium text-white hover:bg-blue-700 disabled:opacity-50" disabled={saving}>
			{saving ? 'Saving…' : initial ? 'Save changes' : 'Add profile'}
		</button>
		<button type="button" class="rounded-md px-4 py-1.5 text-stone-600 hover:bg-stone-100" onclick={oncancel}>Cancel</button>
	</div>
</form>
