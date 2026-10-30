<script lang="ts">
	import {
		presets as seed,
		num,
		ACTIVE_DEVICES,
		PLATFORMS,
		PLATFORM_SHARE,
		type Preset,
	} from '$lib/mock';

	const STEPS = [0, 5, 25, 50, 100];

	let list = $state<Preset[]>(structuredClone(seed));
	let currentId = $state<string | null>(null);
	let usersText = $state('');
	const current = $derived(list.find((p) => p.id === currentId) ?? null);

	function select(p: Preset) {
		currentId = p.id;
		usersText = p.users.join('\n');
	}

	select(list[1]);

	/** Devices the preset is meant to reach: the percent of the chosen platforms plus the named people. */
	function target(p: Preset): number {
		const share = p.platforms.reduce((s, x) => s + PLATFORM_SHARE[x], 0);
		return Math.round((ACTIVE_DEVICES * share * p.percent) / 100) + p.users.length;
	}

	function status(p: Preset): { text: string; cls: string } {
		if (p.paused) return { text: 'пауза', cls: 'warn' };
		if (p.percent === 100 && p.platforms.length === PLATFORMS.length) return { text: 'на всех', cls: 'ok' };
		if (p.percent > 0) return { text: `${p.percent}%`, cls: 'on' };
		if (p.users.length > 0) return { text: `тест · ${p.users.length}`, cls: 'dim' };
		return { text: 'черновик', cls: 'dim' };
	}

	function toggle(p: Preset, x: (typeof PLATFORMS)[number]) {
		p.platforms = p.platforms.includes(x) ? p.platforms.filter((y) => y !== x) : [...p.platforms, x];
	}
</script>

<div class="head">
	<h1>пресеты</h1>
</div>

<section class="card">
	<table class="rows">
		<thead>
			<tr><th>пресет</th><th>режим</th><th>получили</th><th class="r">статус</th></tr>
		</thead>
		<tbody>
			{#each list as p (p.id)}
				{@const t = target(p)}
				{@const st = status(p)}
				<tr class="pick" class:sel={p.id === currentId} onclick={() => select(p)}>
					<td>{p.name} <span class="dim">v{p.version}</span></td>
					<td class="dim">{p.mode === 'bypass' ? 'обход' : 'через прокси'}</td>
					<td>
						<div class="got">
							<div class="track"><i style="width: {t ? Math.min(100, (p.received / t) * 100) : 0}%"></i></div>
							<span><b>{num(p.received)}</b><span class="dim">&nbsp;/ {num(t)}</span></span>
						</div>
					</td>
					<td class="r {st.cls}">{st.text}</td>
				</tr>
			{/each}
		</tbody>
	</table>
</section>

{#if current}
	<section class="card editor">
		<h2>{current.name} <span class="dim">v{current.version}</span></h2>

		<div class="grid">
			<span class="dim">доля</span>
			<div class="chips">
				{#each STEPS as s (s)}
					<button class="chip" class:on={current.percent === s} onclick={() => current && (current.percent = s)}>{s}%</button>
				{/each}
				<input class="field pct" type="number" min="0" max="100" bind:value={current.percent} />
			</div>

			<span class="dim">платформы</span>
			<div class="chips">
				{#each PLATFORMS as x (x)}
					<button class="chip" class:on={current.platforms.includes(x)} onclick={() => current && toggle(current, x)}>{x}</button>
				{/each}
			</div>

			<span class="dim">челы</span>
			<textarea
				class="field mono"
				rows="3"
				placeholder="id устройства или @ник, по одному в строке"
				bind:value={usersText}
				oninput={(e) =>
					current &&
					(current.users = e.currentTarget.value
						.split('\n')
						.map((s) => s.trim())
						.filter(Boolean))}
			></textarea>

			<span class="dim">охват</span>
			<span>~<b>{num(target(current))}</b> устройств</span>
		</div>

		<div class="actions">
			<button class="btn" onclick={() => current && (current.paused = !current.paused)}>
				{current.paused ? 'продолжить' : 'пауза'}
			</button>
		</div>

		<div class="content">
			<div>
				<div class="dim">домены · {current.domains.length}</div>
				<div class="mono">{current.domains.join('\n')}</div>
			</div>
			<div>
				<div class="dim">приложения · {current.apps.length}</div>
				<div class="mono">{current.apps.join('\n') || '—'}</div>
			</div>
		</div>
	</section>
{/if}

<style>
	.pick {
		cursor: pointer;
	}

	.pick:hover td,
	.pick.sel td {
		background: oklch(0.21 0 0);
	}

	.got {
		display: flex;
		align-items: center;
		gap: 10px;
		font-size: 12px;
	}

	.track {
		width: 120px;
		height: 3px;
		border-radius: 2px;
		background: var(--border);
		overflow: hidden;
	}

	.track i {
		display: block;
		height: 100%;
		background: var(--primary);
	}

	.r {
		white-space: nowrap;
	}

	td.on {
		color: var(--primary);
	}

	.grid {
		display: grid;
		grid-template-columns: 90px minmax(0, 420px);
		gap: 12px 16px;
		align-items: center;
	}

	.pct {
		width: 64px;
		padding: 3px 8px;
		font-size: 11px;
	}

	textarea {
		resize: vertical;
	}

	.actions {
		margin: 16px 0;
		padding-left: 106px;
	}

	.content {
		display: flex;
		gap: 48px;
		padding-top: 14px;
		border-top: 1px solid var(--border-subtle);
		font-size: 12px;
	}

	.content .mono {
		margin-top: 6px;
		white-space: pre-line;
		color: var(--text-2);
	}
</style>
