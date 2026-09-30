<script lang="ts">
	import { untrack } from 'svelte';
	import { defaultThinkingStyle, THINKING_LEVELS, THINKING_STYLES } from '$lib/params';
	import type { ChatParams, ThinkingLevel } from '$lib/types';

	let {
		params = $bindable(),
		error = $bindable(''),
		baseUrl = '',
		compact = false
	}: {
		params: ChatParams;
		/** Set when a field is invalid (e.g. extra body JSON). */
		error?: string;
		/** Source base URL, used to pick a default thinking mapping. */
		baseUrl?: string;
		/** Hide the system prompt and extra body. */
		compact?: boolean;
	} = $props();

	// The extra body is edited as text; resync when a new params object
	// arrives (session loaded, profile applied), not on every keystroke.
	let extraText = $state('');
	let synced: ChatParams | undefined;
	$effect.pre(() => {
		const p = params;
		if (p === synced) return;
		synced = p;
		untrack(() => {
			extraText = p.extra_body && Object.keys(p.extra_body).length ? JSON.stringify(p.extra_body, null, 2) : '';
			error = '';
		});
	});

	function onExtraInput(text: string) {
		extraText = text;
		if (!text.trim()) {
			params.extra_body = null;
			error = '';
			return;
		}
		try {
			const v = JSON.parse(text);
			if (!v || typeof v !== 'object' || Array.isArray(v)) {
				error = 'Extra body must be a JSON object';
				return;
			}
			params.extra_body = v;
			error = '';
		} catch {
			error = 'Extra body is not valid JSON';
		}
	}

	function numOrNull(v: string, int = false): number | null {
		if (v.trim() === '') return null;
		const n = Number(v);
		if (Number.isNaN(n)) return null;
		return int ? Math.trunc(n) : n;
	}

	function setThinking(level: ThinkingLevel) {
		params.thinking = level;
		if (level && !params.thinking_style) params.thinking_style = defaultThinkingStyle(baseUrl);
	}

	const style = $derived(THINKING_STYLES.find((s) => s.value === params.thinking_style));
	const input = 'w-full rounded-md border border-stone-300 bg-white px-2 py-1 text-sm';
	const label = 'mb-1 block text-xs text-stone-500';
</script>

<div class="space-y-3 text-sm">
	<div>
		<span class={label}>Thinking</span>
		<div class="flex flex-wrap gap-1" role="radiogroup" aria-label="Thinking level">
			{#each THINKING_LEVELS as l (l.value)}
				<button
					type="button"
					role="radio"
					aria-checked={params.thinking === l.value}
					class="rounded-md border px-2 py-1 text-xs {params.thinking === l.value
						? 'border-blue-500 bg-blue-50 text-blue-900'
						: 'border-stone-300 bg-white text-stone-600 hover:bg-stone-50'}"
					onclick={() => setThinking(l.value)}
				>
					{l.label}
				</button>
			{/each}
		</div>
		{#if params.thinking}
			<label class="mt-2 block">
				<span class={label}>Send as</span>
				<select class={input} bind:value={params.thinking_style}>
					{#each THINKING_STYLES as s (s.value)}<option value={s.value}>{s.label}</option>{/each}
				</select>
			</label>
			{#if style}<p class="mt-1 text-xs text-stone-400">{style.hint}</p>{/if}
		{/if}
	</div>

	<div class="grid grid-cols-2 gap-2">
		<label class="block">
			<span class={label}>Temperature</span>
			<input class={input} type="number" step="0.1" min="0" max="2" placeholder="default" value={params.temperature ?? ''} oninput={(e) => (params.temperature = numOrNull(e.currentTarget.value))} />
		</label>
		<label class="block">
			<span class={label}>Max tokens</span>
			<input class={input} type="number" min="1" step="1" placeholder="default" value={params.max_tokens ?? ''} oninput={(e) => (params.max_tokens = numOrNull(e.currentTarget.value, true))} />
		</label>
	</div>

	{#if !compact}
		<label class="block">
			<span class={label}>System prompt</span>
			<textarea class={input} rows="3" bind:value={params.system_prompt}></textarea>
		</label>
		<label class="block">
			<span class={label}>Extra body (JSON, merged into the request)</span>
			<textarea
				class="{input} font-mono text-xs {error ? 'border-red-400' : ''}"
				rows="4"
				placeholder={'{"top_k": 20}'}
				value={extraText}
				oninput={(e) => onExtraInput(e.currentTarget.value)}
			></textarea>
		</label>
		{#if error}<p class="text-xs text-red-700">{error}</p>{/if}
	{/if}
</div>
