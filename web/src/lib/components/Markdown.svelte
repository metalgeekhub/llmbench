<script lang="ts">
	import { renderMarkdown } from '$lib/markdown';

	let { source, class: className = '' }: { source: string; class?: string } = $props();

	const html = $derived(renderMarkdown(source));
</script>

<!-- Sanitized in renderMarkdown. -->
<div
	class="prose prose-stone max-w-none prose-pre:bg-stone-900 prose-pre:text-stone-100 prose-code:before:content-none prose-code:after:content-none [&_:not(pre)>code]:rounded [&_:not(pre)>code]:bg-stone-100 [&_:not(pre)>code]:px-1 [&_:not(pre)>code]:py-0.5 [&_:not(pre)>code]:font-normal [&_table]:block [&_table]:overflow-x-auto {className}"
>
	{@html html}
</div>
