<script lang="ts">
	import { goto, replaceState } from '$app/navigation';
	import { page } from '$app/state';
	import { onMount } from 'svelte';
	import {
		ApiError,
		createDefinition,
		definitionVersions,
		getDefinition,
		getRun,
		listProfiles,
		listSources,
		runDefinition,
		sourceModels,
		startRun,
		updateDefinition
	} from '$lib/api';
	import { formatTokens } from '$lib/dims';
	import { duration, int } from '$lib/format';
	import { defaultThinkingStyle, THINKING_STYLES } from '$lib/params';
	import { seriesColor } from '$lib/palette';
	import type { BenchmarkConfig, LoadModel, Profile, SLO, Source, TestType, ThinkingStyle } from '$lib/types';

	interface TargetRow {
		source_id: string;
		model: string;
		profile_id: string;
	}

	const TYPES: { value: TestType; title: string; desc: string }[] = [
		{ value: 'concurrency_sweep', title: 'Load sweep', desc: 'Several user counts or request rates: find where throughput plateaus.' },
		{ value: 'context_sweep', title: 'Context sweep', desc: 'Several prompt lengths: see how TTFT and speed degrade with context.' },
		{ value: 'thinking_comparison', title: 'Thinking comparison', desc: 'Thinking off/low/medium/high: latency and token cost of reasoning.' },
		{ value: 'matrix', title: 'Matrix', desc: 'Any combination of load, context length and thinking level.' },
		{ value: 'single', title: 'Single benchmark', desc: 'N requests at one load level.' }
	];
	const LEVELS = ['off', 'low', 'medium', 'high'] as const;

	let sources = $state<Source[]>([]);
	let profiles = $state<Profile[]>([]);
	let modelOptions = $state<Record<string, string[]>>({});
	let modelErrors = $state<Record<string, string>>({});

	let name = $state('');
	let type = $state<TestType>('concurrency_sweep');
	let targets = $state<TargetRow[]>([]);
	let loadModel = $state<LoadModel>('closed');
	let singleLoad = $state('8');
	let usersText = $state('1, 2, 4, 8, 16, 32');
	let ratesText = $state('1, 2, 5, 10');
	let maxInFlight = $state('256');
	let contextText = $state('1024, 4096, 16384');
	let thinkingLevels = $state<string[]>(['off', 'high']);
	let thinkingStyle = $state<ThinkingStyle>('');
	let requestsPerCell = $state('50');
	let durationSeconds = $state('');
	let warmup = $state('2');
	let promptMode = $state<'fixed' | 'synthetic'>('fixed');
	let promptText = $state('Write a detailed story about a lighthouse keeper who discovers a message in a bottle.');
	let inputTokens = $state('1024');
	let systemPrompt = $state('');
	let maxTokens = $state('256');
	let temperature = $state('');
	let ignoreEos = $state(false);
	let cacheBust = $state(true);
	let timeout = $state('');
	let maxErrorRate = $state('50');
	let extraBody = $state('');
	let showAdvanced = $state(false);
	let sloTTFT = $state('');
	let sloTPOT = $state('');
	let sloE2E = $state('');
	let sloTPS = $state('');

	// Set while editing a saved definition (?def=id).
	let defId = $state('');
	let defVersion = $state(0);
	let description = $state('');

	let error = $state('');
	let notice = $state('');
	let starting = $state(false);
	let saving = $state(false);

	onMount(async () => {
		try {
			[sources, profiles] = await Promise.all([listSources(), listProfiles()]);
		} catch (e) {
			error = (e as Error).message;
			return;
		}
		const from = page.url.searchParams.get('from');
		const def = page.url.searchParams.get('def');
		if (def) {
			try {
				const d = await getDefinition(def);
				const restore = Number(page.url.searchParams.get('version'));
				const old = restore && restore !== d.version ? (await definitionVersions(def)).find((v) => v.version === restore) : undefined;
				prefill(old?.config ?? d.config);
				name = d.name;
				description = d.description;
				defId = d.id;
				defVersion = d.version;
				if (old) {
					notice = `Loaded version ${old.version}. Saving makes it version ${d.version + 1}, the latest.`;
				} else {
					try {
						savedSnapshot = snapshot(buildConfig());
					} catch {
						// incomplete config (e.g. a deleted source): any save is a change
					}
				}
			} catch (e) {
				error = `Could not load the saved definition: ${(e as Error).message}`;
			}
		} else if (from) {
			try {
				prefill((await getRun(from)).config);
			} catch (e) {
				error = `Could not load run to re-run: ${(e as Error).message}`;
			}
		}
		if (targets.length === 0 && sources.length) addTarget();
		for (const t of targets) loadModels(t.source_id);
	});

	function prefill(c: BenchmarkConfig) {
		name = c.name ? `${c.name} (re-run)` : '';
		type = c.type;
		targets = c.targets.map((t) => ({ source_id: t.source_id, model: t.model, profile_id: t.profile_id ?? '' }));
		loadModel = c.load_model ?? 'closed';
		const loads = loadModel === 'open' ? (c.arrival_rates ?? []) : (c.concurrency ?? []);
		if (loadMultiFor(c.type)) {
			if (loadModel === 'open') ratesText = loads.join(', ');
			else usersText = loads.join(', ');
		} else singleLoad = String(loads[0] ?? 1);
		if (c.max_in_flight) maxInFlight = String(c.max_in_flight);
		if (c.context_lengths?.length) contextText = c.context_lengths.join(', ');
		else if (c.type === 'matrix') contextText = '';
		thinkingLevels = c.thinking_levels?.length ? [...c.thinking_levels] : c.type === 'matrix' ? [] : thinkingLevels;
		thinkingStyle = c.thinking_style ?? '';
		requestsPerCell = c.requests_per_cell ? String(c.requests_per_cell) : '';
		durationSeconds = c.duration_seconds ? String(c.duration_seconds) : '';
		warmup = String(c.warmup_requests);
		promptMode = c.prompt.mode;
		promptText = c.prompt.text || promptText;
		inputTokens = String(c.prompt.input_tokens || 1024);
		systemPrompt = c.prompt.system_prompt;
		maxTokens = c.max_tokens == null ? '' : String(c.max_tokens);
		temperature = c.temperature == null ? '' : String(c.temperature);
		ignoreEos = c.ignore_eos;
		cacheBust = c.cache_bust;
		timeout = c.timeout_seconds ? String(c.timeout_seconds) : '';
		maxErrorRate = c.max_error_rate ? String(Math.round(c.max_error_rate * 100)) : '';
		extraBody = c.extra_body && Object.keys(c.extra_body).length ? JSON.stringify(c.extra_body, null, 2) : '';
		showAdvanced = !!extraBody || !!systemPrompt || !!temperature;
		const slo = c.slo ?? {};
		sloTTFT = slo.ttft_ms == null ? '' : String(slo.ttft_ms);
		sloTPOT = slo.tpot_ms == null ? '' : String(slo.tpot_ms);
		sloE2E = slo.e2e_ms == null ? '' : String(slo.e2e_ms);
		sloTPS = slo.min_output_tps == null ? '' : String(slo.min_output_tps);
	}

	async function loadModels(sourceId: string) {
		if (!sourceId || modelOptions[sourceId]) return;
		try {
			const ml = await sourceModels(sourceId);
			modelOptions[sourceId] = ml.models;
			if (ml.error) modelErrors[sourceId] = ml.error;
			for (const t of targets) {
				if (t.source_id === sourceId && !t.model && ml.models.length) t.model = ml.models[0];
			}
		} catch (e) {
			modelErrors[sourceId] = (e as Error).message;
			modelOptions[sourceId] = [];
		}
	}

	function addTarget() {
		const source_id = targets.at(-1)?.source_id ?? sources[0]?.id ?? '';
		targets.push({ source_id, model: '', profile_id: '' });
		loadModels(source_id);
		const opts = modelOptions[source_id];
		if (opts) {
			const used = new Set(targets.filter((t) => t.source_id === source_id).map((t) => t.model));
			targets[targets.length - 1].model = opts.find((m) => !used.has(m)) ?? opts[0] ?? '';
		}
	}

	function changeSource(t: TargetRow, value: string) {
		if (value.startsWith('profile:')) {
			t.profile_id = value.slice('profile:'.length);
			return;
		}
		t.profile_id = '';
		t.source_id = value;
		t.model = modelOptions[value]?.[0] ?? '';
		loadModels(value);
	}

	function toggleLevel(l: string) {
		thinkingLevels = thinkingLevels.includes(l) ? thinkingLevels.filter((x) => x !== l) : [...thinkingLevels, l];
	}

	// Which dimensions the chosen type sweeps.
	const loadMultiFor = (t: TestType) => t === 'concurrency_sweep' || t === 'matrix';
	const loadMulti = $derived(loadMultiFor(type));
	const showContext = $derived(type === 'context_sweep' || type === 'matrix');
	const showThinking = $derived(type === 'thinking_comparison' || type === 'matrix');

	// Inputs bound to type="number" hold numbers once edited, so accept both.
	function parseList(text: string | number, integer: boolean): number[] | null {
		const vals = String(text ?? '').split(/[\s,]+/).filter(Boolean).map(Number);
		if (vals.some((v) => !Number.isFinite(v) || v <= 0 || (integer && !Number.isInteger(v)))) return null;
		return [...new Set(vals)].sort((a, b) => a - b);
	}

	const loads = $derived.by(() => {
		const integer = loadModel === 'closed';
		if (!loadMulti) return parseList(singleLoad, integer) ?? [];
		return parseList(loadModel === 'open' ? ratesText : usersText, integer) ?? [];
	});
	const contexts = $derived(showContext ? (parseList(contextText, true) ?? []) : []);
	const levels = $derived(showThinking ? LEVELS.filter((l) => thinkingLevels.includes(l)) : []);
	const cellCount = $derived(targets.length * loads.length * Math.max(contexts.length, 1) * Math.max(levels.length, 1));
	const perCell = $derived(Number(requestsPerCell) || 0);
	const perCellDuration = $derived(Number(durationSeconds) || 0);
	const warmupCount = $derived(Number(warmup) || 0);
	const firstSource = $derived.by(() => {
		const t = targets[0];
		if (!t) return undefined;
		const sid = t.profile_id ? profiles.find((p) => p.id === t.profile_id)?.source_id : t.source_id;
		return sources.find((s) => s.id === sid);
	});
	const effectiveStyle = $derived(thinkingStyle || defaultThinkingStyle(firstSource?.base_url));

	const planProblem = $derived.by(() => {
		if (targets.length === 0) return 'Add a model.';
		if (loads.length === 0) return loadModel === 'open' ? 'Enter request rates, e.g. 1, 2, 5.' : 'Enter whole numbers of users, e.g. 1, 2, 4, 8.';
		if (type === 'context_sweep' && contexts.length === 0) return 'Enter context lengths in tokens, e.g. 1024, 4096.';
		if (showContext && contextText.trim() && contexts.length === 0) return 'Context lengths must be whole numbers of tokens.';
		if (type === 'thinking_comparison' && levels.length === 0) return 'Pick the thinking levels to compare.';
		if (perCell <= 0 && perCellDuration <= 0) return 'Set requests per step or a duration.';
		if (cellCount > 256) return `That is ${cellCount} steps; the limit is 256.`;
		return '';
	});

	function num(v: string | number | null): number | null {
		const t = String(v ?? '').trim();
		return t === '' ? null : Number(t);
	}

	function buildSLO(): SLO | null {
		const slo: SLO = {};
		const fields: [keyof SLO, string, string][] = [
			['ttft_ms', sloTTFT, 'TTFT'],
			['tpot_ms', sloTPOT, 'TPOT'],
			['e2e_ms', sloE2E, 'E2E'],
			['min_output_tps', sloTPS, 'Output speed']
		];
		for (const [key, text, what] of fields) {
			const v = num(text);
			if (v == null) continue;
			if (!Number.isFinite(v) || v <= 0) throw new Error(`${what} target must be a positive number`);
			slo[key] = v;
		}
		return Object.keys(slo).length ? slo : null;
	}

	function buildConfig(): BenchmarkConfig {
		let extra: Record<string, unknown> | null = null;
		if (extraBody.trim()) {
			const v = JSON.parse(extraBody);
			if (!v || typeof v !== 'object' || Array.isArray(v)) throw new Error('Extra body must be a JSON object');
			extra = v;
		}
		if (planProblem) throw new Error(planProblem);
		const errRate = num(maxErrorRate);
		return {
			// A definition's runs are named after it ("Name (vN)").
			name: defId ? '' : name.trim(),
			type,
			targets: targets.map((t) =>
				t.profile_id ? { source_id: '', model: '', profile_id: t.profile_id } : { source_id: t.source_id, model: t.model.trim() }
			),
			prompt: {
				mode: contexts.length ? 'synthetic' : promptMode,
				text: contexts.length ? '' : promptText,
				input_tokens: Number(inputTokens) || 0,
				system_prompt: systemPrompt
			},
			max_tokens: num(maxTokens),
			temperature: num(temperature),
			ignore_eos: ignoreEos,
			extra_body: extra,
			load_model: loadModel,
			concurrency: loadModel === 'closed' ? loads : [],
			arrival_rates: loadModel === 'open' ? loads : [],
			max_in_flight: loadModel === 'open' ? Number(maxInFlight) || 0 : 0,
			context_lengths: contexts,
			thinking_levels: levels,
			thinking_style: levels.length ? effectiveStyle : '',
			requests_per_cell: perCell,
			duration_seconds: perCellDuration,
			warmup_requests: warmupCount,
			cache_bust: cacheBust,
			timeout_seconds: num(timeout) ?? 0,
			max_error_rate: errRate == null ? 0 : errRate / 100,
			slo: buildSLO()
		};
	}

	function tryBuild(): BenchmarkConfig | null {
		try {
			return buildConfig();
		} catch (err) {
			error = err instanceof SyntaxError ? 'Extra body is not valid JSON' : (err as Error).message;
			return null;
		}
	}

	// The last saved definition input, so unchanged forms don't create versions.
	let savedSnapshot = '';

	function snapshot(config: BenchmarkConfig): string {
		return JSON.stringify({ name: name.trim(), description: description.trim(), config: { ...config, name: '' } });
	}

	/** Saves the form as a definition: a new one, or the next version of the one being edited. */
	async function save(): Promise<boolean> {
		error = '';
		notice = '';
		if (!name.trim()) {
			error = 'Give the definition a name.';
			return false;
		}
		const config = tryBuild();
		if (!config) return false;
		config.name = '';
		const snap = snapshot(config);
		if (defId && snap === savedSnapshot) {
			notice = `No changes since version ${defVersion}.`;
			return true;
		}
		saving = true;
		try {
			const input = { name: name.trim(), description, config };
			const d = defId ? await updateDefinition(defId, input) : await createDefinition(input);
			defId = d.id;
			defVersion = d.version;
			savedSnapshot = snap;
			replaceState(`/benchmarks/new?def=${d.id}`, {});
			notice = `Saved “${d.name}” as version ${d.version}.`;
			return true;
		} catch (err) {
			error = (err as Error).message;
			return false;
		} finally {
			saving = false;
		}
	}

	async function start(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		// Editing a definition: save a new version first so the run links to it.
		if (defId && !(await save())) return;
		const config = defId ? null : tryBuild();
		if (!defId && !config) return;
		starting = true;
		try {
			const run = defId ? await runDefinition(defId) : await startRun(config!);
			goto(`/benchmarks/${run.id}`);
		} catch (err) {
			error = (err as Error).message;
			if (err instanceof ApiError && err.status === 409) error += ' (see the Benchmarks list)';
		} finally {
			starting = false;
		}
	}

	const input = 'w-full rounded-md border border-stone-300 bg-white px-2 py-1.5 text-sm';
	const label = 'mb-1 block text-xs font-medium text-stone-600';
	const hint = 'mt-1 block text-xs text-stone-400';
</script>

<div class="h-full overflow-y-auto">
	<form class="mx-auto max-w-3xl space-y-6 p-6 text-sm" onsubmit={start}>
		<div>
			<a href="/benchmarks" class="text-xs text-stone-500 hover:text-stone-800">← Benchmarks</a>
			<h1 class="mt-1 text-xl font-semibold">
				{#if defId}Edit saved definition <span class="text-sm font-normal text-stone-500">v{defVersion}</span>{:else}New benchmark{/if}
			</h1>
			{#if defId}
				<p class="mt-1 text-xs text-stone-500">Saving creates version {defVersion + 1}; earlier versions and their runs are kept.</p>
			{/if}
		</div>

		{#if sources.length === 0 && !error}
			<p class="rounded-md border border-dashed border-stone-300 p-4 text-stone-600">
				You need a source first. <a href="/sources" class="text-blue-700 underline">Add one</a>.
			</p>
		{/if}

		<section class="space-y-4 rounded-lg border border-stone-200 bg-white p-5">
			<div class="grid grid-cols-2 gap-2 sm:grid-cols-3">
				{#each TYPES as t (t.value)}
					<label class="flex cursor-pointer gap-2 rounded-md border p-2.5 {type === t.value ? 'border-blue-500 bg-blue-50/40' : 'border-stone-200'}">
						<input type="radio" bind:group={type} value={t.value} class="mt-0.5" />
						<span>
							<span class="block font-medium">{t.title}</span>
							<span class="text-xs text-stone-500">{t.desc}</span>
						</span>
					</label>
				{/each}
			</div>
			<label class="block">
				<span class={label}>Name</span>
				<input class={input} placeholder={defId ? 'definition name' : 'optional — generated from the type and model'} bind:value={name} />
				{#if defId}<span class={hint}>Runs are named “{name.trim() || '…'} (vN)”.</span>{/if}
			</label>
			{#if defId}
				<label class="block">
					<span class={label}>Description</span>
					<textarea class={input} rows="2" placeholder="optional — what this test is for" bind:value={description}></textarea>
				</label>
			{/if}
		</section>

		<section class="space-y-3 rounded-lg border border-stone-200 bg-white p-5">
			<h2 class="font-semibold">Models</h2>
			{#each targets as t, i (i)}
				<div class="flex items-center gap-2">
					<span class="h-3 w-3 shrink-0 rounded-sm" style="background: {seriesColor(i)}" aria-hidden="true"></span>
					<select
						class="{input} w-56"
						value={t.profile_id ? `profile:${t.profile_id}` : t.source_id}
						onchange={(e) => changeSource(t, e.currentTarget.value)}
						aria-label="Source or profile"
					>
						<optgroup label="Sources">
							{#each sources as s (s.id)}<option value={s.id}>{s.name}</option>{/each}
						</optgroup>
						{#if profiles.length}
							<optgroup label="Profiles">
								{#each profiles as p (p.id)}<option value="profile:{p.id}">{p.name}</option>{/each}
							</optgroup>
						{/if}
					</select>
					{#if t.profile_id}
						{@const p = profiles.find((x) => x.id === t.profile_id)}
						<span class="{input} truncate border-dashed bg-stone-50 text-stone-600" title="Model and settings come from the profile">
							<span class="font-mono">{p?.model ?? 'missing profile'}</span>
							{#if p?.params.thinking}<span class="text-xs text-blue-800"> · thinking {p.params.thinking}</span>{/if}
						</span>
					{:else}
						<input class="{input} font-mono" list="models-{i}" placeholder="model" bind:value={t.model} aria-label="Model" />
						<datalist id="models-{i}">
							{#each modelOptions[t.source_id] ?? [] as m (m)}<option value={m}></option>{/each}
						</datalist>
					{/if}
					<button type="button" class="px-2 text-stone-400 hover:text-stone-700 disabled:opacity-30" disabled={targets.length === 1} aria-label="Remove model" onclick={() => targets.splice(i, 1)}>✕</button>
				</div>
				{#if modelErrors[t.source_id]}
					<p class="ml-5 text-xs text-amber-800">⚠ Could not list models: {modelErrors[t.source_id]}. Type the model name.</p>
				{/if}
			{/each}
			{#if targets.length < 8}
				<button type="button" class="text-xs text-blue-700 underline" onclick={addTarget}>+ Add model to compare</button>
			{/if}
		</section>

		<section class="grid grid-cols-1 gap-4 rounded-lg border border-stone-200 bg-white p-5 sm:grid-cols-2">
			<div class="flex flex-wrap items-center justify-between gap-2 sm:col-span-2">
				<h2 class="font-semibold">Load</h2>
				<div class="flex rounded-md border border-stone-300 p-0.5 text-xs" role="radiogroup" aria-label="Load model">
					{#each [['closed', 'Concurrent users'], ['open', 'Requests per second']] as [v, l] (v)}
						<button type="button" role="radio" aria-checked={loadModel === v} class="rounded px-2.5 py-1 {loadModel === v ? 'bg-stone-800 text-white' : 'text-stone-600'}" onclick={() => (loadModel = v as LoadModel)}>{l}</button>
					{/each}
				</div>
			</div>
			<p class="text-xs text-stone-500 sm:col-span-2">
				{#if loadModel === 'closed'}
					Closed loop: each simulated user sends its next request as soon as the previous one finishes. Models "N users chatting".
				{:else}
					Open loop: requests arrive at a fixed rate (random Poisson arrivals) no matter how fast the server answers. Models real traffic and reveals queueing.
				{/if}
			</p>
			{#if loadMulti}
				<label class="block">
					<span class={label}>{loadModel === 'closed' ? 'Concurrent users per step' : 'Requests per second per step'}</span>
					{#if loadModel === 'closed'}
						<input class="{input} font-mono" required bind:value={usersText} />
					{:else}
						<input class="{input} font-mono" required bind:value={ratesText} />
					{/if}
					<span class={hint}>Comma-separated{loadModel === 'closed' ? ', e.g. 1, 2, 4, 8, 16, 32' : '; decimals allowed, e.g. 0.5, 1, 2, 5'}.</span>
				</label>
			{:else}
				<label class="block">
					<span class={label}>{loadModel === 'closed' ? 'Concurrent users' : 'Requests per second'}</span>
					<input class={input} type="number" min={loadModel === 'closed' ? 1 : 0.01} step={loadModel === 'closed' ? 1 : 'any'} required bind:value={singleLoad} />
				</label>
			{/if}
			{#if loadModel === 'open'}
				<label class="block">
					<span class={label}>Max requests in flight</span>
					<input class={input} type="number" min="1" max="4096" bind:value={maxInFlight} />
					<span class={hint}>Arrivals beyond this wait; the delay is reported as schedule lag.</span>
				</label>
			{/if}
			<label class="block">
				<span class={label}>Warm-up requests per step</span>
				<input class={input} type="number" min="0" max="1000" bind:value={warmup} />
				<span class={hint}>Sent before measuring; excluded from results.</span>
			</label>
			<label class="block">
				<span class={label}>Requests per step</span>
				<input class={input} type="number" min="0" placeholder="no limit" bind:value={requestsPerCell} />
			</label>
			<label class="block">
				<span class={label}>Max duration per step (seconds)</span>
				<input class={input} type="number" min="0" step="any" placeholder="no limit" bind:value={durationSeconds} />
				<span class={hint}>Each step ends at whichever limit comes first.</span>
			</label>
		</section>

		{#if showContext}
			<section class="space-y-2 rounded-lg border border-stone-200 bg-white p-5">
				<h2 class="font-semibold">Context lengths</h2>
				<input class="{input} font-mono" placeholder={type === 'matrix' ? 'optional, e.g. 1024, 4096, 16384' : '1024, 4096, 16384, 32768'} bind:value={contextText} />
				<p class="text-xs text-stone-400">
					Input tokens per step. Prompts are synthesized to these exact lengths (cl100k tokenizer; your server's count may differ slightly).
					{#if contexts.length}Steps: {contexts.map(formatTokens).join(', ')} tokens.{/if}
				</p>
			</section>
		{/if}

		{#if showThinking}
			<section class="space-y-3 rounded-lg border border-stone-200 bg-white p-5">
				<h2 class="font-semibold">Thinking levels</h2>
				<div class="flex flex-wrap gap-2">
					{#each LEVELS as l (l)}
						<label class="flex cursor-pointer items-center gap-1.5 rounded-md border px-3 py-1.5 {thinkingLevels.includes(l) ? 'border-blue-500 bg-blue-50/40' : 'border-stone-300'}">
							<input type="checkbox" checked={thinkingLevels.includes(l)} onchange={() => toggleLevel(l)} />
							{l}
						</label>
					{/each}
				</div>
				<label class="block">
					<span class={label}>Send as</span>
					<select class={input} value={effectiveStyle} onchange={(e) => (thinkingStyle = e.currentTarget.value as ThinkingStyle)}>
						{#each THINKING_STYLES as s (s.value)}<option value={s.value}>{s.label}</option>{/each}
					</select>
					<span class={hint}>{THINKING_STYLES.find((s) => s.value === effectiveStyle)?.hint} Profiles with their own style keep it.</span>
				</label>
				{#if type === 'matrix' && levels.length === 0}
					<p class="text-xs text-stone-400">None selected: each model's own thinking setting is used.</p>
				{/if}
			</section>
		{/if}

		<section class="space-y-4 rounded-lg border border-stone-200 bg-white p-5">
			<h2 class="font-semibold">Prompt</h2>
			{#if contexts.length}
				<p class="text-xs text-stone-500">Synthetic prompts at the context lengths above.</p>
			{:else}
				<div class="flex gap-4">
					<label class="flex items-center gap-1.5"><input type="radio" value="fixed" bind:group={promptMode} /> Fixed text</label>
					<label class="flex items-center gap-1.5"><input type="radio" value="synthetic" bind:group={promptMode} /> Synthetic, exact length</label>
				</div>
				{#if promptMode === 'fixed'}
					<textarea class={input} rows="3" required bind:value={promptText}></textarea>
				{:else}
					<label class="block">
						<span class={label}>Input length (tokens)</span>
						<input class="{input} w-40" type="number" min="16" required bind:value={inputTokens} />
						<span class={hint}>Filler text sized with a cl100k tokenizer; your server's tokenizer may count slightly differently.</span>
					</label>
				{/if}
			{/if}
			<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
				<label class="block">
					<span class={label}>Max output tokens</span>
					<input class={input} type="number" min="1" placeholder="server default" bind:value={maxTokens} />
				</label>
				<label class="block pt-5">
					<span class="flex items-center gap-1.5"><input type="checkbox" bind:checked={ignoreEos} /> Always generate max tokens</span>
					<span class={hint}>Sends <code>ignore_eos</code> (vLLM/SGLang) for exact output lengths.</span>
				</label>
			</div>
		</section>

		<section class="space-y-3 rounded-lg border border-stone-200 bg-white p-5">
			<div>
				<h2 class="font-semibold">Goodput targets (SLO) <span class="text-xs font-normal text-stone-400">optional</span></h2>
				<p class="mt-1 text-xs text-stone-500">
					A request is <i>good</i> when it succeeds and meets every target you set. Results then show goodput (good requests per second) and
					the highest load that still meets the targets. You can also set or change targets after the run.
				</p>
			</div>
			<div class="grid grid-cols-2 gap-4 sm:grid-cols-4">
				<label class="block">
					<span class={label}>Max TTFT (ms)</span>
					<input class={input} type="number" min="0" step="any" placeholder="—" bind:value={sloTTFT} />
				</label>
				<label class="block">
					<span class={label}>Max TPOT (ms)</span>
					<input class={input} type="number" min="0" step="any" placeholder="—" bind:value={sloTPOT} />
				</label>
				<label class="block">
					<span class={label}>Max E2E (ms)</span>
					<input class={input} type="number" min="0" step="any" placeholder="—" bind:value={sloE2E} />
				</label>
				<label class="block">
					<span class={label}>Min output tok/s</span>
					<input class={input} type="number" min="0" step="any" placeholder="—" bind:value={sloTPS} />
				</label>
			</div>
		</section>

		<section class="rounded-lg border border-stone-200 bg-white p-5">
			<button type="button" class="font-semibold" onclick={() => (showAdvanced = !showAdvanced)}>
				{showAdvanced ? '▾' : '▸'} Options
			</button>
			{#if showAdvanced}
				<div class="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2">
					<label class="flex items-center gap-1.5 sm:col-span-2">
						<input type="checkbox" bind:checked={cacheBust} /> Bust prefix caches
						<span class="text-xs text-stone-400">— adds a unique marker to every prompt so KV/prefix caching doesn't inflate results</span>
					</label>
					<label class="block">
						<span class={label}>Stop if error rate exceeds (%)</span>
						<input class={input} type="number" min="0" max="100" placeholder="never" bind:value={maxErrorRate} />
					</label>
					<label class="block">
						<span class={label}>Per-request timeout (seconds)</span>
						<input class={input} type="number" min="0" placeholder="source default" bind:value={timeout} />
					</label>
					<label class="block">
						<span class={label}>Temperature</span>
						<input class={input} type="number" step="0.1" min="0" max="2" placeholder="server default" bind:value={temperature} />
					</label>
					<label class="block sm:col-span-2">
						<span class={label}>System prompt</span>
						<textarea class={input} rows="2" bind:value={systemPrompt}></textarea>
					</label>
					<label class="block sm:col-span-2">
						<span class={label}>Extra body (JSON, merged into every request)</span>
						<textarea class="{input} font-mono text-xs" rows="3" placeholder={'{"top_k": 20}'} bind:value={extraBody}></textarea>
					</label>
				</div>
			{/if}
		</section>

		<div class="flex flex-wrap items-center justify-between gap-4 rounded-lg border border-stone-200 bg-stone-100/60 px-5 py-4">
			<p class="text-stone-700">
				{#if planProblem}
					{planProblem}
				{:else}
					<b>{cellCount}</b> step{cellCount === 1 ? '' : 's'}
					<span class="text-stone-500">
						({[
							`${targets.length} model${targets.length === 1 ? '' : 's'}`,
							contexts.length ? `${contexts.length} context length${contexts.length === 1 ? '' : 's'}` : '',
							levels.length ? `${levels.length} thinking level${levels.length === 1 ? '' : 's'}` : '',
							`${loads.length} load level${loads.length === 1 ? '' : 's'}`
						]
							.filter(Boolean)
							.join(' × ')})
					</span>
					{#if perCell > 0}· <b>{int(cellCount * perCell)}</b> measured requests{/if}
					{#if warmupCount > 0}+ {int(cellCount * warmupCount)} warm-up{/if}
					{#if perCellDuration > 0}· at most {duration(cellCount * perCellDuration)} of measuring{/if}
				{/if}
			</p>
			<div class="flex items-center gap-2">
				<button
					type="button"
					class="rounded-md border border-stone-300 bg-white px-4 py-2 font-medium text-stone-700 hover:bg-stone-50 disabled:opacity-50"
					title={defId ? 'Save the changes as a new version' : 'Save this configuration to run again later (needs a name)'}
					disabled={saving || starting || !!planProblem}
					onclick={save}
				>
					{saving ? 'Saving…' : defId ? `Save as v${defVersion + 1}` : 'Save as definition'}
				</button>
				<button type="submit" class="rounded-md bg-blue-600 px-5 py-2 font-medium text-white hover:bg-blue-700 disabled:opacity-50" disabled={starting || saving || !!planProblem}>
					{starting ? 'Starting…' : defId ? 'Save & start' : 'Start benchmark'}
				</button>
			</div>
		</div>

		{#if notice}
			<p class="rounded bg-green-50 px-3 py-2 text-green-800">✓ {notice} <a href="/benchmarks" class="underline">Saved definitions</a></p>
		{/if}
		{#if error}
			<p class="rounded bg-red-50 px-3 py-2 text-red-800">⚠ {error}</p>
		{/if}
	</form>
</div>
