<script lang="ts">
	import WinChips from '$lib/WinChips.svelte';
	import { errors, num, ago, PLATFORMS, type Platform, type Win } from '$lib/mock';

	let win = $state<Win>('24h');
	let platform = $state<Platform | null>(null);
	let query = $state('');
	let open = $state<string | null>(null);

	const list = $derived(
		errors(win).filter(
			(e) =>
				(!platform || e.platforms.includes(platform)) &&
				(e.message + e.where).toLowerCase().includes(query.trim().toLowerCase()),
		),
	);
	const total = $derived(list.reduce((s, e) => s + e.count, 0));
</script>

<div class="head">
	<h1>ошибки</h1>
	<div class="chips">
		<button class="chip" class:on={!platform} onclick={() => (platform = null)}>все</button>
		{#each PLATFORMS as p (p)}
			<button class="chip" class:on={platform === p} onclick={() => (platform = p)}>{p}</button>
		{/each}
	</div>
	<WinChips bind:value={win} />
</div>

<section class="card">
	<div class="top">
		<input class="field" placeholder="поиск по тексту или модулю" bind:value={query} />
		<span class="dim">{list.length} групп · <b>{num(total)}</b> раз</span>
	</div>

	{#if list.length === 0}
		<p class="empty">ничего не нашлось</p>
	{:else}
		<table class="rows">
			<thead>
				<tr><th class="r">раз</th><th>ошибка</th><th class="r">устройств</th><th class="r">последняя</th></tr>
			</thead>
			<tbody>
				{#each list as e (e.id)}
					<tr class="err" onclick={() => (open = open === e.id ? null : e.id)}>
						<td class="r"><b>{num(e.count)}</b></td>
						<td>
							<div class="msg">{e.message}</div>
							<div class="dim">{e.where} · {e.platforms.join(', ')} · {e.versions.join(', ')}</div>
							{#if open === e.id}
								<pre>{e.stack}</pre>
							{/if}
						</td>
						<td class="r"><b>{num(e.devices)}</b></td>
						<td class="r dim">{ago(e.last)}</td>
					</tr>
				{/each}
			</tbody>
		</table>
	{/if}
</section>

<style>
	.top {
		display: flex;
		align-items: center;
		gap: 16px;
		margin-bottom: 14px;
	}

	.top .field {
		max-width: 320px;
	}

	.top span {
		margin-left: auto;
		font-size: 11px;
	}

	.err {
		cursor: pointer;
	}

	.err:hover td {
		background: oklch(0.21 0 0);
	}

	.r {
		width: 1%;
		white-space: nowrap;
	}

	.msg {
		font-family: var(--mono);
		font-size: 12px;
	}

	.rows .dim {
		font-size: 11px;
	}

	pre {
		margin-top: 8px;
		padding: 10px 12px;
		background: var(--inset);
		border: 1px solid var(--border-subtle);
		border-radius: 6px;
		font-family: var(--mono);
		font-size: 11px;
		color: var(--text-2);
		white-space: pre-wrap;
	}
</style>
