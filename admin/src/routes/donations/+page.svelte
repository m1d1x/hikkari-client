<script lang="ts">
	import Chart from '$lib/Chart.svelte';
	import WinChips from '$lib/WinChips.svelte';
	import { donations, num, rub, ago, type Win } from '$lib/mock';

	let win = $state<Win>('30d');
	const d = $derived(donations(win));
</script>

<div class="head">
	<h1>донаты</h1>
	<WinChips bind:value={win} />
</div>

<section class="card">
	<div class="split">
		<div>
			<div class="big">{num(d.total)}<span class="unit">₽</span></div>
			<div class="what"><i class="sw" style="background: var(--ok)"></i>за окно</div>
			<div class="breakdown">
				<div><span>донатов</span><b>{num(d.count)}</b></div>
				<div><span>людей</span><b>{num(d.donors)}</b></div>
				<div><span>средний</span><b>{rub(d.avg)}</b></div>
				<div class="sep"><span>прогноз на месяц</span><b>{rub(d.monthForecast)}</b></div>
			</div>
		</div>
		<Chart
			buckets={d.buckets}
			{win}
			fmt={rub}
			series={[{ label: 'сумма', color: 'var(--ok)', values: d.series, primary: true }]}
		/>
	</div>
</section>

<section class="card">
	<h2>последние</h2>
	<table class="rows">
		<tbody>
			{#each d.list as x, i (i)}
				<tr>
					<td class="dim when">{ago(x.ts)}</td>
					<td>{x.who ?? 'аноним'}</td>
					<td class="dim">{x.note}</td>
					<td class="dim">{x.via}</td>
					<td class="r"><b>{rub(x.rub)}</b></td>
				</tr>
			{/each}
		</tbody>
	</table>
</section>

<style>
	.when {
		width: 70px;
	}
</style>
