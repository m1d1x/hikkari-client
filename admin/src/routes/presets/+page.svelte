<script lang="ts">
	import { tick } from 'svelte';
	import { page } from '$app/state';
	import { db } from '$lib/store.svelte';
	import {
		num,
		pct,
		STAGES,
		SIDES,
		RULE_KINDS,
		ACTIVE_DEVICES,
		type Group,
		type Preset,
		type Rules,
		type Side,
	} from '$lib/mock';

	let currentId = $state<string | undefined>();
	// The textarea holds its own text so a trailing newline survives typing;
	// it is reset only when another preset is picked.
	let testersText = $state('');
	let nameEl = $state<HTMLInputElement | null>(null);
	const p = $derived(db.presets.find((x) => x.id === currentId) ?? db.presets[0]);

	function select(id: string | undefined) {
		currentId = id;
		testersText = db.presets.find((x) => x.id === id)?.testers.join('\n') ?? '';
	}

	select(page.url.searchParams.get('p') ?? db.presets[0]?.id);

	const stageLabel = (i: number) => (i === 0 ? 'тестеры' : i === STAGES.length - 1 ? 'все' : `${STAGES[i]}%`);
	const latest = (x: Preset) => x.next ?? x.current;
	const groupOf = (id: string) => db.groups.find((g) => g.id === id);
	const ruleCount = (r: Rules) => RULE_KINDS.reduce((n, k) => n + r[k.key].length, 0);

	function plural(n: number, [one, few, many]: [string, string, string]): string {
		const d = n % 10;
		const h = n % 100;
		return `${n} ${d === 1 && h !== 11 ? one : d >= 2 && d <= 4 && (h < 12 || h > 14) ? few : many}`;
	}

	/** Rules added and dropped between two states of one group, OS named for the per-OS lists. */
	function ruleDiff(was: Rules, now: Rules): string[] {
		return RULE_KINDS.flatMap(({ key, os }) => {
			const tag = os ? ` (${os})` : '';
			return [
				...now[key].filter((v) => !was[key].includes(v)).map((v) => `+ ${v}${tag}`),
				...was[key].filter((v) => !now[key].includes(v)).map((v) => `− ${v}${tag}`),
			];
		});
	}

	/** What a release would change against the latest version: groups in and out, and edits inside groups. */
	function changes(x: Preset): { text: string; kind: 'add' | 'drop' | 'edit' }[] {
		const base = latest(x);
		return SIDES.flatMap(({ key, label }) => {
			const was = base?.[key] ?? [];
			const out: { text: string; kind: 'add' | 'drop' | 'edit' }[] = [];
			for (const id of x[key]) {
				const g = groupOf(id);
				const old = was.find((o) => o.id === id);
				if (!g) continue;
				if (!old) out.push({ text: `+ ${g.name} → ${label}`, kind: 'add' });
				else {
					const d = ruleDiff(old.rules, g.rules);
					if (d.length) out.push({ text: `${g.name}: ${d.join('  ')}`, kind: 'edit' });
				}
			}
			for (const old of was) {
				if (!x[key].includes(old.id)) out.push({ text: `− ${old.name} из «${label}»`, kind: 'drop' });
			}
			return out;
		});
	}

	const diff = $derived(p ? changes(p) : []);
	const free = $derived(db.groups.filter((g) => p && !p.bypass.includes(g.id) && !p.proxy.includes(g.id)));
	const worse = $derived(!!p?.next && p.stage > 0 && p.errPct.next > p.errPct.current * 1.5);

	function status(x: Preset): { text: string; cls: string } {
		if (x.next) return x.paused ? { text: 'пауза', cls: 'warn' } : { text: stageLabel(x.stage), cls: 'on' };
		if (!x.current) return { text: 'черновик', cls: 'dim' };
		if (changes(x).length) return { text: 'изменён', cls: 'dim' };
		return { text: `v${x.current.v}`, cls: 'dim' };
	}

	/** Devices the stage is meant to reach: its share of everyone plus the testers. */
	function target(x: Preset): number {
		return Math.round((ACTIVE_DEVICES * STAGES[x.stage]) / 100) + x.testers.length;
	}

	async function create() {
		const x: Preset = {
			id: Math.random().toString(36).slice(2, 8),
			name: '',
			bypass: [],
			proxy: [],
			current: null,
			next: null,
			stage: STAGES.length - 1,
			testers: [],
			paused: false,
			received: 0,
			errPct: { next: 0, current: 0 },
		};
		db.presets.push(x);
		select(x.id);
		await tick();
		nameEl?.focus();
	}

	function remove() {
		if (!confirm(`удалить пресет «${p.name || 'без названия'}»? у людей он пропадёт`)) return;
		db.presets = db.presets.filter((x) => x.id !== p.id);
		select(db.presets[0]?.id);
	}

	function addGroup(side: Side, id: string) {
		if (id) p[side] = [...p[side], id];
	}

	function dropGroup(side: Side, id: string) {
		p[side] = p[side].filter((x) => x !== id);
	}

	/** Freezes the groups as they are now into the next version. */
	function release() {
		const freeze = (ids: string[]) =>
			ids.map(groupOf).filter((g): g is Group => !!g).map((g) => $state.snapshot(g));
		p.next = { v: (latest(p)?.v ?? 0) + 1, bypass: freeze(p.bypass), proxy: freeze(p.proxy) };
		p.stage = 0;
		p.received = 0;
		p.paused = false;
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

	/** Drops the version being rolled out; the groups stay as edited, so it can be fixed and released again. */
	function rollback() {
		p.next = null;
		p.stage = STAGES.length - 1;
		p.paused = false;
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
		<div class="dim sub">набор групп, который клиент забирает с сервера; раскатывается целиком</div>
	</div>
</div>

<div class="presets">
	<section class="card list">
		<button class="btn add" onclick={create}>+ пресет</button>
		{#each db.presets as x (x.id)}
			{@const st = status(x)}
			<button class="item" class:on={x.id === currentId} onclick={() => select(x.id)}>
				<div class="line">
					<span class:dim={!x.name}>{x.name || 'без названия'}</span>
					<span class="st {st.cls}">{st.text}</span>
				</div>
				<div class="dim meta">{plural(x.bypass.length + x.proxy.length, ['группа', 'группы', 'групп'])}</div>
			</button>
		{/each}
	</section>

	{#if p}
		<section class="card">
			<div class="top">
				<input class="name" placeholder="название пресета" bind:this={nameEl} bind:value={p.name} />
				<button class="btn" onclick={remove}>удалить</button>
			</div>

			<div class="sides">
				{#each SIDES as s (s.key)}
					<div>
						<div class="label">{s.label}</div>
						<div class="groups">
							{#each p[s.key] as id (id)}
								{@const g = groupOf(id)}
								{#if g}
									<span class="g">
										<a href="/groups?g={g.id}">{g.name || 'без названия'}</a>
										<small>{ruleCount(g.rules)}</small>
										<button onclick={() => dropGroup(s.key, id)} title="убрать">×</button>
									</span>
								{/if}
							{/each}
							{#if free.length}
								<select
									class="pick"
									onchange={(e) => {
										addGroup(s.key, e.currentTarget.value);
										e.currentTarget.value = '';
									}}
								>
									<option value="">+ группа</option>
									{#each free as g (g.id)}
										<option value={g.id}>{g.name || 'без названия'}</option>
									{/each}
								</select>
							{/if}
						</div>
					</div>
				{/each}
			</div>


			{#if diff.length}
				<div class="block">
					<div class="release">
						<div class="label">что уйдёт людям в v{(latest(p)?.v ?? 0) + 1}</div>
						{#if p.next}
							<span class="dim hint">v{p.next.v} ещё раскатывается: заверши или откати, потом выпускай</span>
						{:else}
							<button class="btn primary" onclick={release}>выпустить v{(latest(p)?.v ?? 0) + 1}</button>
						{/if}
					</div>
					<div class="diff">
						{#each diff as d (d.text)}
							<div class:ok={d.kind === 'add'} class:bad={d.kind === 'drop'}>{d.text}</div>
						{/each}
					</div>
				</div>
			{/if}

			{#if p.next}
				<div class="block">
					<div class="release">
						<span class="label">раскатка v{p.next.v}</span>
						<div class="actions">
							<button class="btn" onclick={rollback}>откатить</button>
							<button class="btn" onclick={() => (p.paused = !p.paused)}>{p.paused ? 'продолжить' : 'пауза'}</button>
							<button class="btn primary" onclick={advance}>
								{p.stage < STAGES.length - 1 ? `дальше → ${stageLabel(p.stage + 1)}` : 'завершить'}
							</button>
						</div>
					</div>
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
			{:else if p.current}
				<div class="block dim">v{p.current.v} у всех · {num(p.received)} устройств</div>
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
		grid-template-columns: 220px minmax(0, 1fr);
		gap: 16px;
		align-items: start;
	}

	/* ---------- preset list ---------- */

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
		margin-bottom: 18px;
	}

	.name {
		flex: 1;
		font-size: 15px;
		font-weight: 600;
	}

	.name::placeholder {
		color: var(--text-3);
	}

	.label {
		font-size: 11px;
		color: var(--text-3);
	}

	.sides {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 24px;
	}

	.sides .label {
		margin-bottom: 8px;
	}

	.groups {
		display: flex;
		flex-wrap: wrap;
		gap: 6px;
	}

	.g {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		padding: 3px 4px 3px 10px;
		border: 1px solid var(--border);
		border-radius: 6px;
		background: var(--inset);
		font-size: 12px;
	}

	.g a:hover {
		color: var(--primary);
	}

	.g small {
		font-family: var(--mono);
		font-size: 10px;
		color: var(--text-3);
	}

	.g button {
		padding: 0 4px;
		color: var(--text-3);
	}

	.g button:hover {
		color: var(--bad);
	}

	.pick {
		padding: 3px 8px;
		border: 1px dashed var(--border);
		border-radius: 6px;
		background: none;
		color: var(--text-2);
		font: inherit;
		font-size: 12px;
		outline: none;
		cursor: pointer;
	}

	.pick option {
		background: var(--surface);
		color: var(--text);
	}

	.block {
		margin-top: 18px;
		padding-top: 14px;
		border-top: 1px solid var(--border-subtle);
	}

	.form {
		display: grid;
		grid-template-columns: 90px minmax(0, 420px);
		gap: 10px 16px;
		align-items: center;
	}

	.release {
		display: flex;
		align-items: center;
		gap: 16px;
		margin-bottom: 10px;
	}

	.release .btn,
	.release .hint {
		margin-left: auto;
	}

	.hint {
		font-size: 11px;
	}

	.diff {
		display: flex;
		flex-direction: column;
		gap: 3px;
		font-family: var(--mono);
		font-size: 12px;
		color: var(--text-2);
		white-space: pre-wrap;
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
		margin-top: 4px;
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
