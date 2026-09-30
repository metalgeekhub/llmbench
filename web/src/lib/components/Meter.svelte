<script lang="ts">
	import { STATUS } from '$lib/palette';

	let {
		ratio,
		label,
		goodAbove = 0.99,
		warnAbove = 0.95
	}: {
		/** 0–1, higher is better. */
		ratio: number;
		/** Accessible description, e.g. "99% of requests succeeded". */
		label: string;
		goodAbove?: number;
		warnAbove?: number;
	} = $props();

	// Severity is carried by the fill, and repeated by the icon so color is
	// never the only signal.
	const state = $derived(ratio >= goodAbove ? 'good' : ratio >= warnAbove ? 'warning' : 'critical');
	const fill = $derived(STATUS[state]);
	const icon = $derived(state === 'good' ? '✓' : '⚠');
</script>

<div class="flex items-center gap-2" role="meter" aria-valuemin="0" aria-valuemax="100" aria-valuenow={Math.round(ratio * 100)} aria-label={label}>
	<div class="h-2 flex-1 overflow-hidden rounded-full" style="background: {fill}33">
		<div class="h-full rounded-full" style="width: {Math.max(0, Math.min(1, ratio)) * 100}%; background: {fill}"></div>
	</div>
	<span class="text-xs text-stone-600" aria-hidden="true">{icon}</span>
</div>
