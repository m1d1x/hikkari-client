<script lang="ts">
	import Chart from '$lib/Chart.svelte';
	import WinChips from '$lib/WinChips.svelte';
	import { overview, num, pct, PLATFORM_COLOR, type Win } from '$lib/mock';

	let win = $state<Win>('7d');
	const d = $derived(overview(win));
</script>

<div class="head">
	<h1>обзор</h1>
	<WinChips bind:value={win} />
</div>

<section class="card">
	<div class="split">
		<div>
			<div class="big">{num(d.people.active)}</div>
			<div class="what"><i class="sw" style="background: var(--primary)"></i>активных людей</div>
			<div class="breakdown">
				<div><span><i class="sw" style="background: var(--ok)"></i>новых</span><b>{num(d.people.fresh)}</b></div>
				<div><span>вернулись</span><b>{num(d.people.returning)}</b></div>
				<div class="sep"><span>установок за всё время</span><b>{num(d.people.total)}</b></div>
			</div>
		</div>
		<Chart
			buckets={d.buckets}
			{win}
			series={[
				{ label: 'активные', color: 'var(--primary)', values: d.people.series.active, primary: true },
				{ label: 'новые', color: 'var(--ok)', values: d.people.series.fresh },
			]}
		/>
	</div>
	<div class="strip">
		<span>сессий<b>{num(d.sessions.count)}</b></span>
		<span>сессия, медиана<b>{d.sessions.medianMin} мин</b></span>
		<span>подключений успешно<b>{pct(d.sessions.connectOkPct)}</b></span>
		<span>подключение, p50<b>{(d.sessions.connectP50Ms / 1000).toFixed(1)} с</b></span>
	</div>
</section>

<section class="card">
	<div class="split">
		<div>
			<div class="big">{num(d.downloads.clicks)}</div>
			<div class="what"><i class="sw" style="background: var(--blue)"></i>кликов «скачать»</div>
			<div class="breakdown">
				<div><span><i class="sw" style="background: var(--violet)"></i>первых запусков</span><b>{num(d.downloads.installs)}</b></div>
				<div class="sep"><span>клик → запуск</span><b>{pct(d.downloads.convPct)}</b></div>
			</div>
		</div>
		<Chart
			buckets={d.buckets}
			{win}
			series={[
				{ label: 'клики', color: 'var(--blue)', values: d.downloads.series.clicks, primary: true },
				{ label: 'запуски', color: 'var(--violet)', values: d.downloads.series.installs },
			]}
		/>
	</div>
	<div class="strip">
		<div class="bar">
			{#each d.downloads.platforms as p (p.p)}
				<i style="width: {p.pct}%; background: {PLATFORM_COLOR[p.p]}"></i>
			{/each}
		</div>
		{#each d.downloads.platforms as p (p.p)}
			<span><i class="sw" style="background: {PLATFORM_COLOR[p.p]}"></i> {p.p}<b>{num(p.installs)}</b><span class="dim">&nbsp;· {p.pct.toFixed(0)}%</span></span>
		{/each}
	</div>
</section>

<style>
	.bar {
		flex-basis: 100%;
		display: flex;
		gap: 2px;
		height: 6px;
		margin: 2px 0 4px;
		border-radius: 3px;
		overflow: hidden;
	}

	.bar i {
		opacity: 0.8;
	}
</style>
