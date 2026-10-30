<script lang="ts">
	import {
		presets as seed,
		num,
		pct,
		STAGES,
		ACTIVE_DEVICES,
		PLATFORMS,
		PLATFORM_SHARE,
		type Platform,
		type Preset,
	} from '$lib/mock';

	const MODE = {
		bypass: 'эти сайты и приложения идут мимо vpn, остальное через него',
		proxy: 'через vpn идут только эти сайты и приложения',
	};

	let list = $state<Preset[]>(structuredClone(seed));
	let currentId = $state<string>(seed[0].id);
	let testersText = $state(seed[0].testers.join('\n'));
	const p = $derived(list.find((x) => x.id === currentId) ?? list[0]);

	const stageLabel = (i: number) => (i === 0 ? 'тестеры' : i === STAGES.length - 1 ? 'все' : `${STAGES[i]}%`);

	/** Devices the stage is meant to reach: its share of the chosen platforms plus the testers. */
	function target(x: Preset): number {
		const share = x.platforms.reduce((s, y) => s + PLATFORM_SHARE[y], 0);
		return Math.round((ACTIVE_DEVICES * share * STAGES[x.stage]) / 100) + x.testers.length;
	}

	/** What the next version adds and removes against the current one. */
	const diff = $derived.by(() => {
		if (!p.next || !p.current) return [];
		const was = new Set([...p.current.domains, ...p.current.apps]);
		const now = new Set([...p.next.domains, ...p.next.apps]);
		return [
			...[...now].filter((x) => !was.has(x)).map((x) => ({ x, add: true })),
			...[...was].filter((x) => !now.has(x)).map((x) => ({ x, add: false })),
		];
	});

	/** The lists the client gets: the version being rolled out, else the current one. */
	const shown = $derived(p.next ?? p.current);

	const worse = $derived(p.next !== null && p.stage > 0 && p.errPct.next > p.errPct.current * 1.5);

	function select(x: Preset) {
		currentId = x.id;
		testersText = x.testers.join('\n');
	}

	function advance() {
		if (!p.next) return;
		if (p.stage < STAGES.length - 1) {
			p.stage++;
			p.received = Math.round(target(p) * 0.1);
		} else {
			p.current = p.next;
			p.next = null;
		}
		p.paused = false;
	}

	function rollback() {
		p.next = null;
		p.stage = STAGES.length - 1;
		p.paused = false;
	}

	function togglePlatform(x: Platform) {
		p.platforms = p.platforms.includes(x) ? p.platforms.filter((y) => y !== x) : [...p.platforms, x];
	}

	function setTesters(text: string) {
		p.testers = text
			.split('\n')
			.map((s) => s.trim())
			.filter(Boolean);
	}
</script>

<div class="head">
	<div>
		<h1>пресеты</h1>
		<div class="dim sub">готовые настройки маршрутизации, которые клиент забирает с сервера</div>
	</div>
</div>

<section class="card">
	<table class="rows">
		<tbody>
			{#each list as x (x.id)}
				<tr class="pick" class:sel={x.id === currentId} onclick={() => select(x)}>
					<td>{x.name}</td>
					<td class="dim">{x.mode === 'bypass' ? 'мимо vpn' : 'через vpn'}</td>
					<td class="dim">
						{#if x.next}
							{x.current ? `v${x.current.v} → ` : ''}v{x.next.v}
						{:else if x.current}
							v{x.current.v}
						{/if}
					</td>
					<td class="r">
						{#if !x.next}
							<span class="dim">у всех</span>
						{:else if x.paused}
							<span class="warn">пауза · {stageLabel(x.stage)}</span>
						{:else}
							<span class="on">{stageLabel(x.stage)}</span>
						{/if}
					</td>
				</tr>
			{/each}
		</tbody>
	</table>
</section>

<section class="card">
	<div class="top">
		<div>
			<h2>{p.name}</h2>
			<div class="dim">{MODE[p.mode]}</div>
		</div>
		{#if p.next}
			<div class="actions">
				<button class="btn" onclick={rollback}>откатить</button>
				<button class="btn" onclick={() => (p.paused = !p.paused)}>{p.paused ? 'продолжить' : 'пауза'}</button>
				<button class="btn primary" onclick={advance}>
					{p.stage < STAGES.length - 1 ? `дальше → ${stageLabel(p.stage + 1)}` : 'завершить'}
				</button>
			</div>
		{/if}
	</div>

	{#if p.next}
		{#if p.current}
			<div class="block">
				<div class="label">что меняется в v{p.next.v}</div>
				<div class="diff">
					{#each diff as d (d.x)}
						<span class:ok={d.add} class:bad={!d.add}>{d.add ? '+' : '−'} {d.x}</span>
					{/each}
				</div>
			</div>
		{/if}

		<div class="block">
			<div class="label">раскатка</div>
			<div class="stages">
				{#each STAGES as _, i (i)}
					<div class="stage" class:done={i < p.stage} class:now={i === p.stage}>
						<i></i>
						<span>{stageLabel(i)}</span>
					</div>
				{/each}
			</div>
			<div class="figures">
				<span>получили<b>{num(p.received)}</b><span class="dim">&nbsp;из ~{num(target(p))}</span></span>
				{#if p.stage > 0}
					<span>ошибки у них<b class:bad={worse}>{pct(p.errPct.next)}</b></span>
					<span>у остальных<b>{pct(p.errPct.current)}</b></span>
				{/if}
			</div>
		</div>

		<div class="block who">
			<div class="label">платформы</div>
			<div class="chips">
				{#each PLATFORMS as x (x)}
					<button class="chip" class:on={p.platforms.includes(x)} onclick={() => togglePlatform(x)}>{x}</button>
				{/each}
			</div>
			<div class="label">тестеры</div>
			<textarea
				class="field mono"
				rows="3"
				placeholder="id устройства или @ник, по одному в строке"
				bind:value={testersText}
				oninput={(e) => setTesters(e.currentTarget.value)}
			></textarea>
		</div>
	{:else}
		<p class="empty">v{p.current?.v} у всех, новой версии нет</p>
	{/if}

	{#if shown}
		<div class="block content">
			<div>
				<div class="label">сайты · {shown.domains.length}</div>
				<div class="mono">{shown.domains.join('\n')}</div>
			</div>
			<div>
				<div class="label">приложения · {shown.apps.length}</div>
				<div class="mono">{shown.apps.join('\n') || '—'}</div>
			</div>
		</div>
	{/if}
</section>

<style>
	.sub {
		font-size: 12px;
	}

	.rows tr:first-child td {
		border-top: none;
	}

	.pick {
		cursor: pointer;
	}

	.pick:hover td,
	.pick.sel td {
		background: oklch(0.21 0 0);
	}

	.r {
		text-align: right;
		white-space: nowrap;
	}

	.on {
		color: var(--primary);
	}

	.top {
		display: flex;
		align-items: flex-start;
		gap: 16px;
	}

	.top h2 {
		margin-bottom: 2px;
	}

	.actions {
		margin-left: auto;
		display: flex;
		gap: 6px;
	}

	.block {
		margin-top: 18px;
		padding-top: 14px;
		border-top: 1px solid var(--border-subtle);
	}

	.label {
		margin-bottom: 8px;
		font-size: 11px;
		color: var(--text-3);
	}

	.diff {
		display: flex;
		flex-wrap: wrap;
		gap: 6px 16px;
		font-family: var(--mono);
		font-size: 12px;
	}

	/* rollout stepper: dots on a line, done ones filled */
	.stages {
		display: flex;
		max-width: 520px;
	}

	.stage {
		flex: 1;
		position: relative;
		font-size: 11px;
		color: var(--text-3);
	}

	.stage i {
		position: relative;
		z-index: 1;
		display: block;
		width: 10px;
		height: 10px;
		margin-bottom: 6px;
		border-radius: 50%;
		border: 1.5px solid var(--border);
		background: var(--card);
	}

	.stage:not(:last-child)::after {
		content: '';
		position: absolute;
		top: 4px;
		left: 10px;
		right: 0;
		height: 2px;
		background: var(--border);
	}

	.stage.done::after,
	.stage.done i {
		background: var(--primary);
		border-color: var(--primary);
	}

	.stage.now i {
		border-color: var(--primary);
		box-shadow: 0 0 0 3px var(--primary-subtle);
	}

	.stage.done,
	.stage.now {
		color: var(--text);
	}

	.figures {
		display: flex;
		gap: 22px;
		margin-top: 14px;
		font-size: 12px;
		color: var(--text-2);
	}

	.figures b {
		margin-left: 4px;
		color: var(--text);
	}

	.who {
		display: grid;
		grid-template-columns: 90px minmax(0, 420px);
		gap: 10px 16px;
		align-items: center;
	}

	.who .label {
		margin: 0;
	}

	textarea {
		resize: vertical;
	}

	.content {
		display: flex;
		gap: 48px;
		font-size: 12px;
	}

	.content .mono {
		white-space: pre-line;
		color: var(--text-2);
	}
</style>
