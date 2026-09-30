<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		label,
		value,
		sub = '',
		children
	}: {
		label: string;
		value: string;
		/** Context line, e.g. which step the value comes from. */
		sub?: string;
		/** Optional visual under the value (e.g. a meter). */
		children?: Snippet;
	} = $props();
</script>

<div class="min-w-0 rounded-lg border border-stone-200 bg-white px-4 py-3">
	<div class="truncate text-xs text-stone-500">{label}</div>
	<div class="mt-0.5 truncate text-2xl font-semibold tracking-tight text-stone-900" title={value}>{value}</div>
	{#if children}<div class="mt-1.5">{@render children()}</div>{/if}
	{#if sub}<div class="mt-0.5 truncate text-xs text-stone-500" title={sub}>{sub}</div>{/if}
</div>
