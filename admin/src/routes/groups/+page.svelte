<script lang="ts">
	import { tick } from 'svelte';
	import { page } from '$app/state';
	import RuleList from './RuleList.svelte';
	import { db } from '$lib/store.svelte';
	import { RULE_KINDS, type Group } from '$lib/mock';

	let currentId = $state(page.url.searchParams.get('g') ?? db.groups[0]?.id);
	let nameEl = $state<HTMLInputElement | null>(null);
	const g = $derived(db.groups.find((x) => x.id === currentId) ?? db.groups[0]);

	const count = (x: Group) => RULE_KINDS.reduce((n, k) => n + x.rules[k.key].length, 0);
	const usedIn = (x: Group) => db.presets.filter((p) => p.bypass.includes(x.id) || p.proxy.includes(x.id));

	async function create() {
		const x: Group = { id: Math.random().toString(36).slice(2, 8), name: '', rules: { domains: [], ips: [], processes: [], folders: [] } };
		db.groups.push(x);
		currentId = x.id;
		await tick();
		nameEl?.focus();
	}

	function remove() {
		const used = usedIn(g);
		const where = used.length ? `\nона уйдёт из пресетов: ${used.map((p) => p.name).join(', ')}` : '';
		if (!confirm(`удалить группу «${g.name || 'без названия'}»?${where}`)) return;
		for (const p of used) {
			p.bypass = p.bypass.filter((id) => id !== g.id);
			p.proxy = p.proxy.filter((id) => id !== g.id);
		}
		db.groups = db.groups.filter((x) => x.id !== g.id);
		currentId = db.groups[0]?.id;
	}
</script>

<div class="head">
	<div>
		<h1>группы</h1>
		<div class="dim sub">всё, что касается одной штуки: домены, ip и приложения стима, дискорда, банков</div>
	</div>
</div>

<div class="groups">
	<section class="card list">
		<button class="btn add" onclick={create}>+ группа</button>
		{#each db.groups as x (x.id)}
			<button class="item" class:on={x.id === currentId} onclick={() => (currentId = x.id)}>
				<div class="line">
					<span class:dim={!x.name}>{x.name || 'без названия'}</span>
					<span class="dim n">{count(x)}</span>
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
			<div class="dim used">
				{#if usedIn(g).length}
					в пресетах:
					{#each usedIn(g) as p, i (p.id)}{i ? ', ' : ' '}<a href="/presets?p={p.id}">{p.name}</a>{/each}
					· правки уйдут людям со следующим выпуском пресета
				{:else}
					ни в одном пресете
				{/if}
			</div>

			<div class="rules">
				{#each RULE_KINDS as k (k.key)}
					<RuleList label={k.label} hint={k.hint} bind:items={g.rules[k.key]} />
				{/each}
			</div>
			<p class="dim note">
				папки — кусок пути, без диска и имени юзера: ловит все exe под ним, где бы ни стояло. так покрываются
				игры лаунчеров (<span class="mono">steamapps/common/</span>) и хелперы внутри .app
				(<span class="mono">Discord.app/</span>)
			</p>
		</section>
	{/if}
</div>

<style>
	.sub {
		font-size: 12px;
	}

	.groups {
		display: grid;
		grid-template-columns: 220px minmax(0, 1fr);
		gap: 16px;
		align-items: start;
	}

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
		padding: 7px 10px;
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

	.n {
		font-family: var(--mono);
		font-size: 11px;
	}

	.top {
		display: flex;
		align-items: center;
		gap: 16px;
	}

	.name {
		flex: 1;
		font-size: 15px;
		font-weight: 600;
	}

	.name::placeholder {
		color: var(--text-3);
	}

	.used {
		margin-top: 2px;
		font-size: 12px;
	}

	.used a {
		color: var(--text-2);
	}

	.used a:hover {
		color: var(--text);
	}

	.note {
		margin-top: 16px;
		font-size: 11px;
	}

	.rules {
		display: grid;
		grid-template-columns: repeat(4, minmax(0, 1fr));
		gap: 24px;
		margin-top: 18px;
		padding-top: 14px;
		border-top: 1px solid var(--border-subtle);
	}
</style>
