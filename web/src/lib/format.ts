// Display formatting for metrics. Null means "undefined for this request".

const DASH = '–';

export function ms(v: number | null | undefined): string {
	if (v == null) return DASH;
	if (v >= 10_000) return `${(v / 1000).toFixed(1)} s`;
	if (v >= 1000) return `${(v / 1000).toFixed(2)} s`;
	if (v >= 100) return `${v.toFixed(0)} ms`;
	return `${v.toFixed(1)} ms`;
}

export function rate(v: number | null | undefined, unit = 'tok/s'): string {
	if (v == null) return DASH;
	return `${v >= 100 ? v.toFixed(0) : v.toFixed(1)} ${unit}`;
}

export function int(v: number | null | undefined): string {
	if (v == null) return DASH;
	return v.toLocaleString();
}

export function dateTime(iso: string): string {
	const d = new Date(iso);
	return d.toLocaleString(undefined, {
		year: 'numeric',
		month: 'short',
		day: 'numeric',
		hour: '2-digit',
		minute: '2-digit',
		second: '2-digit'
	});
}

export function relativeTime(iso: string): string {
	const diff = (Date.now() - new Date(iso).getTime()) / 1000;
	if (diff < 60) return 'just now';
	if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
	if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
	return new Date(iso).toLocaleDateString();
}
