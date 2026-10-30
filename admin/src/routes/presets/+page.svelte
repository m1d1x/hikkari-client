<script lang="ts">
	import { tick } from 'svelte';
	import RuleList from './RuleList.svelte';
	import {
		groups as seed,
		emptyGroup,
		num,
		pct,
		STAGES,
		RULE_KINDS,
		ACTIVE_DEVICES,
		PLATFORMS,
		PLATFORM_SHARE,
		type Group,
		type Platform,
		type Rules,
	} from '$lib/mock';

	let list = $state<Group[]>(structuredClone(seed));
	let currentId = $state(seed[0].id);
	let testersText = $state(seed[0].testers.join('\n'));
	let nameEl = $state<HTMLInputElement | null>(null);
	const g = $derived(list.find((x) => x.id === currentId) ?? list[0]);

	const stageLabel = (i: number) => (i === 0 ? 'тестеры' : i === STAGES.length - 1 ? 'все' : `${STAGES[i]}%`);
	const latest = (x: Group) => x.next ?? x.current;
	const ruleCount = (r: Rules) => r.domains.length + r.ips.length + r.processes.length;

	function plural(n: number, [one, few, many]: [string, string, string]): string {
		const d = n % 10;
		const h = n % 100;
		return `${n} ${d === 1 && h !== 11 ? one : d >= 2 && d <= 4 && (h < 12 || h > 14) ? few : many}`;
	}

	/** Draft against the latest released version: what a release would change. */
	function changes(x: Group): { kind: keyof Rules; v: string; add: boolean }[] {
		const base = latest(x)?.rules;
		return RULE_KINDS.flatMap(({ key }) => {
			const was = new Set(base?.[key] ?? []);
			const now = new Set(x.draft[key]);
			return [
				...[...now].filter((v) => !was.has(v)).map((v) => ({ kind: key, v, add: true })),
				...[...was].filter((v) => !now.has(v)).map((v) => ({ kind: key, v, add: false })),
			];
		});
	}

	const diff = $derived(changes(g));
	const added = $derived(new Set(diff.filter((d) => d.add).map((d) => d.v)));
	const worse = $derived(g.next !== null && g.stage > 0 && g.errPct.next > g.errPct.current * 1.5);

	function status(x: Group): { text: string; cls: string } {
		if (x.next) return x.paused ? { text: 'пауза', cls: 'warn' } : { text: stageLabel(x.stage), cls: 'on' };
		if (!x.current) return { text: 'черновик', cls: 'dim' };
		if (changes(x).length) return { text: 'изменена', cls: 'dim' };
		return { text: `v${x.current.v}`, cls: 'dim' };
	}

	/** Devices the stage is meant to reach: its share of the chosen platforms plus the testers. */
	function target(x: Group): number {
		const share = x.platforms.reduce((s, y) => s + PLATFORM_SHARE[y], 0);
		return Math.round((ACTIVE_DEVICES * share * STAGES[x.stage]) / 100) + x.testers.length;
	}

	function select(x: Group) {
		currentId = x.id;
		testersText = x.testers.join('\n');
	}

	async function create() {
		const x = emptyGroup();
		list.push(x);
		select(x);
		await tick();
		nameEl?.focus();
	}

	function remove() {
		if (!confirm(`удалить группу «${g.name || 'без названия'}»? у всех она пропадёт`)) return;
		list = list.filter((x) => x.id !== g.id);
		if (list.length) select(list[0]);
	}

	function release() {
		g.next = { v: (latest(g)?.v ?? 0) + 1, rules: $state.snapshot(g.draft) };
		g.stage = 0;
		g.received = 0;
		g.paused = false;
	}

	function advance() {
		if (!g.next) return;
		if (g.stage < STAGES.length - 1) {
			g.stage++;
			g.received = Math.round(target(g) * 0.1);
		} else {
			g.current = g.next;
			g.next = null;
		}
		g.paused = false;
	}

	/** Drops the version being rolled out; the draft keeps its rules, so it can be fixed and released again. */
	function rollback() {
		g.next = null;
		g.stage = STAGES.length - 1;
		g.paused = false;
	}

	function togglePlatform(x: Platform) {
		g.platforms = g.platforms.includes(x) ? g.platforms.filter((y) => y !== x) : [...g.platforms, x];
	}

	function setTesters(text: string) {
		g.testers = text
			.split('\n')
			.map((s) => s.trim())
			.filter(Boolean);
	}
</script>

<div class="head">
	<div>
		<h1>пресеты</h1>
		<div class="dim sub">группы правил маршрутизации, которые клиент забирает с сервера</div>
	</div>
</div>

<div class="presets">
	<section class="card list">
		<button class="btn add" onclick={create}>+ группа</button>
		{#each list as x (x.id)}
			{@const st = status(x)}
			<button class="item" class:on={x.id === currentId} onclick={() => select(x)}>
				<div class="line">
					<span class:dim={!x.name}>{x.name || 'без названия'}</span>
					<span class="st {st.cls}">{st.text}</span>
				</div>
				<div class="dim meta">
					{x.mode === 'bypass' ? 'мимо vpn' : 'через vpn'} · {plural(ruleCount(x.draft), ['правило', 'правила', 'правил'])}
				</div>
			</button>
		{/each}
	</section>

	{#if g}
		<section class="card">
			<div class="top">
				<input class="name" placeholder="название группы" bind:this={nameEl} bind:value={g.name} />
				<button class="btn" onclick={remove}>удалить</button>
			</div>

			<div class="form">
				<span class="label">по умолчанию</span>
				<div class="chips">
					<button class="chip" class:on={g.mode === 'bypass'} onclick={() => (g.mode = 'bypass')}>мимо vpn</button>
					<button class="chip" class:on={g.mode === 'proxy'} onclick={() => (g.mode = 'proxy')}>через vpn</button>
				</div>
				<span class="label">платформы</span>
				<div class="chips">
					{#each PLATFORMS as x (x)}
						<button class="chip" class:on={g.platforms.includes(x)} onclick={() => togglePlatform(x)}>{x}</button>
					{/each}
				</div>
			</div>

			<div class="block rules">
				{#each RULE_KINDS as k (k.key)}
					<RuleList label={k.label} hint={k.hint} bind:items={g.draft[k.key]} {added} />
				{/each}
			</div>

			{#if diff.length}
				<div class="block">
					<div class="release">
						<div class="diff">
							{#each diff as d (d.kind + d.v)}
								<span class:ok={d.add} class:bad={!d.add}>{d.add ? '+' : '−'} {d.v}</span>
							{/each}
						</div>
						{#if g.next}
							<span class="dim hint">v{g.next.v} ещё раскатывается: заверши или откати, потом выпускай</span>
						{:else}
							<button class="btn primary" onclick={release}>выпустить v{(latest(g)?.v ?? 0) + 1}</button>
						{/if}
					</div>
				</div>
			{/if}

			{#if g.next}
				<div class="block">
					<div class="rollout-head">
						<span class="label">раскатка v{g.next.v}</span>
						<div class="actions">
							<button class="btn" onclick={rollback}>откатить</button>
							<button class="btn" onclick={() => (g.paused = !g.paused)}>{g.paused ? 'продолжить' : 'пауза'}</button>
							<button class="btn primary" onclick={advance}>
								{g.stage < STAGES.length - 1 ? `дальше → ${stageLabel(g.stage + 1)}` : 'завершить'}
							</button>
						</div>
					</div>
					<div class="stages">
						{#each STAGES as _, i (i)}
							<div class="stage" class:done={i < g.stage} class:now={i === g.stage}>
								<i></i>
								<span>{stageLabel(i)}</span>
							</div>
						{/each}
					</div>
					<div class="figures">
						<span>получили<b>{num(g.received)}</b><span class="dim">&nbsp;из ~{num(target(g))}</span></span>
						{#if g.stage > 0}
							<span>ошибки у них<b class:bad={worse}>{pct(g.errPct.next)}</b></span>
							<span>у остальных<b>{pct(g.errPct.current)}</b></span>
						{/if}
					</div>
					<div class="form testers">
						<span class="label">тестеры</span>
						<textarea
							class="field mono"
							rows="3"
							placeholder="id устройства или @ник, по одному в строке"
							bind:value={testersText}
							oninput={(e) => setTesters(e.currentTarget.value)}
						></textarea>
					</div>
				</div>
			{:else if g.current}
				<div class="block dim">v{g.current.v} у всех · {num(g.received)} устройств</div>
			{/if}
		</section>
	{/if}
</div>

<style>
	.sub {
		font-size: 12px;
	}

	.presets {
		display: grid;
		grid-template-columns: 260px minmax(0, 1fr);
		gap: 16px;
		align-items: start;
	}

	/* ---------- group list ---------- */

	.list {
		padding: 6px;
	}

	.add {
		width: 100%;
		margin-bottom: 4px;
		text-align: left;
	}

	.item {
		display: block;
		width: 100%;
		text-align: left;
		padding: 8px 10px;
		border-radius: 6px;
	}

	.item:hover,
	.item.on {
		background: var(--surface);
	}

	.line {
		display: flex;
		justify-content: space-between;
		gap: 8px;
	}

	.st,
	.meta {
		font-size: 11px;
	}

	.st.on {
		color: var(--primary);
	}

	/* ---------- editor ---------- */

	.top {
		display: flex;
		align-items: center;
		gap: 16px;
		margin-bottom: 14px;
	}

	.name {
		flex: 1;
		font-size: 15px;
		font-weight: 600;
	}

	.name::placeholder {
		color: var(--text-3);
	}

	.form {
		display: grid;
		grid-template-columns: 90px minmax(0, 420px);
		gap: 10px 16px;
		align-items: center;
	}

	.label {
		font-size: 11px;
		color: var(--text-3);
	}

	.block {
		margin-top: 18px;
		padding-top: 14px;
		border-top: 1px solid var(--border-subtle);
	}

	.rules {
		display: grid;
		grid-template-columns: repeat(3, minmax(0, 1fr));
		gap: 24px;
	}

	.release {
		display: flex;
		align-items: center;
		gap: 16px;
	}

	.diff {
		flex: 1;
		display: flex;
		flex-wrap: wrap;
		gap: 4px 16px;
		font-family: var(--mono);
		font-size: 12px;
	}

	.hint {
		font-size: 11px;
	}

	.rollout-head {
		display: flex;
		align-items: center;
		margin-bottom: 12px;
	}

	.actions {
		margin-left: auto;
		display: flex;
		gap: 6px;
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

	.testers {
		margin-top: 16px;
	}

	textarea {
		resize: vertical;
	}
</style>
