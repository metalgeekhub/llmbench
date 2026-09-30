<script lang="ts">
	import { createSource, discoverModels, updateSource } from '$lib/api';
	import type { Source, SourceInput } from '$lib/types';

	let {
		source,
		onsaved,
		oncancel
	}: { source?: Source; onsaved: (s: Source) => void; oncancel: () => void } = $props();

	// Initial values come from `source` once; the parent remounts the form per source.
	const initial = (() => source)();
	const editing = !!initial;

	let name = $state(initial?.name ?? '');
	let type = $state(initial?.type ?? 'openai');
	let baseUrl = $state(initial?.base_url ?? '');
	let apiKey = $state('');
	let clearKey = $state(false);
	let replaceHeaders = $state(!editing);
	let headers = $state<{ key: string; value: string }[]>(
		editing ? (initial?.header_names ?? []).map((key) => ({ key, value: '' })) : []
	);
	let extraBody = $state(
		initial?.extra_body && Object.keys(initial.extra_body).length
			? JSON.stringify(initial.extra_body, null, 2)
			: ''
	);
	let timeout = $state(initial?.timeout_seconds ? String(initial.timeout_seconds) : '');
	let tlsSkipVerify = $state(initial?.tls_skip_verify ?? false);
	let modelsMode = $state<'auto' | 'manual'>(initial && !initial.models_auto ? 'manual' : 'auto');
	let modelsText = $state((initial?.models ?? []).join('\n'));

	let saving = $state(false);
	let fetching = $state(false);
	let error = $state('');
	let fetchInfo = $state('');

	function parseExtraBody(): Record<string, unknown> | null {
		if (!extraBody.trim()) return null;
		const v = JSON.parse(extraBody);
		if (!v || typeof v !== 'object' || Array.isArray(v)) throw new Error('Extra body must be a JSON object');
		return v;
	}

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		let extra: Record<string, unknown> | null;
		try {
			extra = parseExtraBody();
		} catch (err) {
			error = err instanceof SyntaxError ? 'Extra body is not valid JSON' : (err as Error).message;
			return;
		}
		const timeoutSeconds = timeout.trim() ? Number(timeout) : 0;
		if (isNaN(timeoutSeconds) || timeoutSeconds < 0) {
			error = 'Timeout must be a positive number of seconds';
			return;
		}

		const input: SourceInput = {
			name,
			type,
			base_url: baseUrl,
			extra_body: extra,
			timeout_seconds: timeoutSeconds,
			tls_skip_verify: tlsSkipVerify,
			models: modelsMode === 'manual' ? modelsText.split(/[\n,]/).map((m) => m.trim()).filter(Boolean) : [],
			models_auto: modelsMode === 'auto'
		};
		if (!editing) input.api_key = apiKey;
		else if (clearKey) input.api_key = '';
		else if (apiKey) input.api_key = apiKey;
		if (replaceHeaders) {
			input.headers = Object.fromEntries(
				headers.filter((h) => h.key.trim()).map((h) => [h.key.trim(), h.value])
			);
		}

		saving = true;
		try {
			const saved = editing && initial ? await updateSource(initial.id, input) : await createSource(input);
			onsaved(saved);
		} catch (err) {
			error = (err as Error).message;
		} finally {
			saving = false;
		}
	}

	async function fetchModels() {
		if (!initial) return;
		fetching = true;
		fetchInfo = '';
		error = '';
		try {
			const list = await discoverModels(initial.id);
			modelsText = list.join('\n');
			modelsMode = 'manual';
			fetchInfo = `Fetched ${list.length} model${list.length === 1 ? '' : 's'}. Save to keep this list.`;
		} catch (err) {
			error = (err as Error).message;
		} finally {
			fetching = false;
		}
	}

	const inputClass = 'w-full rounded-md border border-stone-300 px-2 py-1.5 text-sm';
</script>

<form class="space-y-4 text-sm" onsubmit={submit}>
	<div class="grid grid-cols-1 gap-4 sm:grid-cols-3">
		<label class="block sm:col-span-2">
			<span class="mb-1 block text-xs font-medium text-stone-600">Name</span>
			<input class={inputClass} required maxlength="64" placeholder="local-vllm" bind:value={name} />
		</label>
		<label class="block">
			<span class="mb-1 block text-xs font-medium text-stone-600">Type</span>
			<select class={inputClass} bind:value={type}>
				<option value="openai">OpenAI-compatible</option>
			</select>
		</label>
	</div>

	<label class="block">
		<span class="mb-1 block text-xs font-medium text-stone-600">Base URL</span>
		<input class="{inputClass} font-mono" required placeholder="http://10.0.0.5:8000/v1" bind:value={baseUrl} />
		<span class="mt-1 block text-xs text-stone-400">Include the API prefix, e.g. <code>/v1</code>. Requests go to <code>{'{base}'}/chat/completions</code>.</span>
	</label>

	<div>
		<label class="block">
			<span class="mb-1 block text-xs font-medium text-stone-600">API key</span>
			<input
				class="{inputClass} font-mono"
				type="password"
				autocomplete="off"
				placeholder={editing && initial?.has_api_key ? '•••••••• (stored — leave empty to keep)' : 'optional'}
				disabled={clearKey}
				bind:value={apiKey}
			/>
		</label>
		{#if editing && initial?.has_api_key}
			<label class="mt-1 flex items-center gap-1.5 text-xs text-stone-600">
				<input type="checkbox" bind:checked={clearKey} /> Remove stored key
			</label>
		{/if}
		<span class="mt-1 block text-xs text-stone-400">Encrypted at rest and never sent back to the browser.</span>
	</div>

	<fieldset>
		<legend class="mb-1 text-xs font-medium text-stone-600">Custom headers</legend>
		{#if editing && !replaceHeaders}
			<p class="text-xs text-stone-500">
				{initial?.header_names.length ? `Stored: ${initial.header_names.join(', ')} (values hidden).` : 'None.'}
				<button type="button" class="ml-1 text-blue-700 underline" onclick={() => (replaceHeaders = true)}>
					Replace headers
				</button>
			</p>
		{:else}
			{#each headers as h, i (i)}
				<div class="mb-1.5 flex gap-2">
					<input class="{inputClass} font-mono" placeholder="X-Api-Token" bind:value={h.key} />
					<input class="{inputClass} font-mono" type="password" autocomplete="off" placeholder="value" bind:value={h.value} />
					<button type="button" class="px-2 text-stone-400 hover:text-stone-700" aria-label="Remove header" onclick={() => headers.splice(i, 1)}>✕</button>
				</div>
			{/each}
			<button type="button" class="text-xs text-blue-700 underline" onclick={() => headers.push({ key: '', value: '' })}>
				+ Add header
			</button>
			{#if editing}<span class="ml-2 text-xs text-stone-400">Saving replaces all stored headers.</span>{/if}
		{/if}
	</fieldset>

	<fieldset>
		<legend class="mb-1 text-xs font-medium text-stone-600">Models</legend>
		<div class="mb-2 flex gap-4">
			<label class="flex items-center gap-1.5"><input type="radio" value="auto" bind:group={modelsMode} /> Auto (fetch from <code>/models</code>)</label>
			<label class="flex items-center gap-1.5"><input type="radio" value="manual" bind:group={modelsMode} /> Manual list</label>
		</div>
		{#if modelsMode === 'manual'}
			<textarea class="{inputClass} font-mono" rows="4" placeholder="one model per line" bind:value={modelsText}></textarea>
		{/if}
		{#if editing}
			<button type="button" class="mt-1 text-xs text-blue-700 underline disabled:opacity-50" disabled={fetching} onclick={fetchModels}>
				{fetching ? 'Fetching…' : 'Fetch list from /models now'}
			</button>
			{#if fetchInfo}<span class="ml-2 text-xs text-stone-500">{fetchInfo}</span>{/if}
		{:else}
			<p class="text-xs text-stone-400">After saving, you can fetch the list from the server to edit it.</p>
		{/if}
	</fieldset>

	<label class="block">
		<span class="mb-1 block text-xs font-medium text-stone-600">Extra body (JSON)</span>
		<textarea class="{inputClass} font-mono text-xs" rows="3" placeholder={'{"chat_template_kwargs": {"enable_thinking": false}}'} bind:value={extraBody}></textarea>
		<span class="mt-1 block text-xs text-stone-400">Merged into every request to this source. Chat parameters can override it.</span>
	</label>

	<div class="flex flex-wrap items-end gap-6">
		<label class="block">
			<span class="mb-1 block text-xs font-medium text-stone-600">Timeout (seconds)</span>
			<input class="{inputClass} w-32" type="number" min="0" step="1" placeholder="600" bind:value={timeout} />
		</label>
		<label class="flex items-center gap-1.5 pb-2">
			<input type="checkbox" bind:checked={tlsSkipVerify} /> Skip TLS certificate verification
		</label>
	</div>

	{#if error}
		<p class="rounded bg-red-50 px-3 py-2 text-red-800">⚠ {error}</p>
	{/if}

	<div class="flex gap-2">
		<button type="submit" class="rounded-md bg-blue-600 px-4 py-1.5 font-medium text-white hover:bg-blue-700 disabled:opacity-50" disabled={saving}>
			{saving ? 'Saving…' : editing ? 'Save changes' : 'Add source'}
		</button>
		<button type="button" class="rounded-md px-4 py-1.5 text-stone-600 hover:bg-stone-100" onclick={oncancel}>Cancel</button>
	</div>
</form>
