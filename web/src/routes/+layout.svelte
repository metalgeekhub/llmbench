<script lang="ts">
	import '../app.css';
	import favicon from '$lib/assets/favicon.svg';
	import { page } from '$app/state';

	let { children } = $props();

	const links = [
		{ href: '/chat', label: 'Chat' },
		{ href: '/history', label: 'History' },
		{ href: '/sources', label: 'Sources' }
	];
</script>

<svelte:head>
	<link rel="icon" href={favicon} />
	<title>LLMBench</title>
</svelte:head>

<div class="flex h-full flex-col bg-stone-50 text-stone-900">
	<header class="flex h-12 shrink-0 items-center gap-6 border-b border-stone-200 bg-white px-4">
		<a href="/chat" class="font-semibold tracking-tight">LLMBench</a>
		<nav class="flex gap-1 text-sm">
			{#each links as link (link.href)}
				<a
					href={link.href}
					class="rounded px-3 py-1.5 {page.url.pathname.startsWith(link.href)
						? 'bg-stone-100 font-medium text-stone-900'
						: 'text-stone-600 hover:text-stone-900'}"
				>
					{link.label}
				</a>
			{/each}
		</nav>
	</header>
	<main class="min-h-0 flex-1">
		{@render children()}
	</main>
</div>
