// Categorical series colors (validated reference palette, light mode).
// Assign by the entity's position in the run config, never by rank, so a
// model keeps its color across charts and filters. Max 8 = runner.MaxTargets.
export const SERIES = [
	'#2a78d6', // blue
	'#eb6834', // orange
	'#1baf7a', // aqua
	'#eda100', // yellow
	'#e87ba4', // magenta
	'#008300', // green
	'#4a3aa7', // violet
	'#e34948' // red
];

export const TEXT_SECONDARY = '#52514e';
export const GRID = '#e7e5e4';

export const seriesColor = (i: number) => SERIES[i % SERIES.length];

// Status colors are reserved for state (never a series) and always ship
// with an icon + label.
export const STATUS = {
	good: '#0ca30c',
	warning: '#fab219',
	serious: '#ec835a',
	critical: '#d03b3b'
};

// Sequential blue ramp (steps 100→700) for magnitude: heatmaps, meters.
export const SEQUENTIAL = [
	'#cde2fb',
	'#b7d3f6',
	'#9ec5f4',
	'#86b6ef',
	'#6da7ec',
	'#5598e7',
	'#3987e5',
	'#2a78d6',
	'#256abf',
	'#1c5cab',
	'#184f95',
	'#104281',
	'#0d366b'
];

/** Ramp color for t in [0, 1]; also says whether text on it should be white. */
export function rampColor(t: number): { color: string; whiteText: boolean } {
	const i = Math.round(Math.max(0, Math.min(1, t)) * (SEQUENTIAL.length - 1));
	return { color: SEQUENTIAL[i], whiteText: i >= 6 };
}
