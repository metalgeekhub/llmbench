<script lang="ts">
	import type { RunStatus } from '$lib/types';

	let { status }: { status: RunStatus } = $props();

	// Icon + label: status is never conveyed by color alone.
	const styles: Record<RunStatus, [string, string]> = {
		pending: ['○ pending', 'bg-stone-100 text-stone-600'],
		running: ['● running', 'bg-blue-50 text-blue-800'],
		completed: ['✓ completed', 'bg-green-50 text-green-800'],
		stopped: ['⏹ stopped', 'bg-stone-100 text-stone-700'],
		aborted: ['⚠ aborted', 'bg-amber-50 text-amber-900'],
		failed: ['✕ failed', 'bg-red-50 text-red-800'],
		skipped: ['– skipped', 'bg-stone-100 text-stone-500'],
		interrupted: ['⚠ interrupted', 'bg-amber-50 text-amber-900']
	};
	const [label, cls] = $derived(styles[status] ?? [status, 'bg-stone-100 text-stone-600']);
</script>

<span class="rounded px-1.5 py-0.5 text-xs font-medium whitespace-nowrap {cls}">
	{#if status === 'running'}<span class="animate-pulse">{label}</span>{:else}{label}{/if}
</span>
