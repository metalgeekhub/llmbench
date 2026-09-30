<script lang="ts">
	import { int } from '$lib/format';

	let { content, syntheticTokens = 0 }: { content: string; syntheticTokens?: number } = $props();

	// Synthetic prompts end with "\n\n<instruction>".
	const instruction = $derived(syntheticTokens ? content.slice(content.lastIndexOf('\n\n') + 2) : '');
</script>

{#if syntheticTokens > 0}
	<div class="text-sm">
		<div class="font-medium text-stone-800">Synthetic prompt · {int(syntheticTokens)} tokens</div>
		<div class="text-stone-600">then: “{instruction}”</div>
		<details class="mt-1">
			<summary class="cursor-pointer text-xs text-stone-500">Show full text ({int(content.length)} characters)</summary>
			<div class="mt-1 max-h-48 overflow-y-auto rounded bg-white/60 p-2 text-xs whitespace-pre-wrap text-stone-600">{content}</div>
		</details>
	</div>
{:else}
	<div class="whitespace-pre-wrap">{content}</div>
{/if}
