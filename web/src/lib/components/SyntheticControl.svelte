<script lang="ts">
	let {
		enabled = $bindable(false),
		tokens = $bindable(4096),
		disabled = false
	}: {
		/** true: send a synthetic prompt of `tokens` tokens instead of typed text. */
		enabled?: boolean;
		tokens?: number;
		disabled?: boolean;
	} = $props();

	const PRESETS = [1024, 4096, 16384, 32768];
	const label = (n: number) => (n % 1024 === 0 ? `${n / 1024}k` : String(n));
</script>

<div class="flex flex-wrap items-center gap-2 text-xs">
	<div class="flex rounded-md border border-stone-300 p-0.5" role="radiogroup" aria-label="Prompt type">
		<button type="button" role="radio" aria-checked={!enabled} {disabled} class="rounded px-2 py-0.5 {!enabled ? 'bg-stone-800 text-white' : 'text-stone-600'}" onclick={() => (enabled = false)}>Text</button>
		<button type="button" role="radio" aria-checked={enabled} {disabled} class="rounded px-2 py-0.5 {enabled ? 'bg-stone-800 text-white' : 'text-stone-600'}" onclick={() => (enabled = true)}>Synthetic length</button>
	</div>
	{#if enabled}
		<label class="flex items-center gap-1">
			<input class="w-24 rounded-md border border-stone-300 px-2 py-0.5 font-mono" type="number" min="16" max="500000" step="1" {disabled} bind:value={tokens} aria-label="Synthetic prompt tokens" />
			<span class="text-stone-500">tokens</span>
		</label>
		{#each PRESETS as p (p)}
			<button type="button" {disabled} class="rounded border px-1.5 py-0.5 {tokens === p ? 'border-blue-500 bg-blue-50 text-blue-900' : 'border-stone-300 text-stone-600 hover:bg-stone-50'}" onclick={() => (tokens = p)}>{label(p)}</button>
		{/each}
		<span class="text-stone-400">Filler text of exactly this length (cl100k); anything you type is appended as the instruction.</span>
	{/if}
</div>
