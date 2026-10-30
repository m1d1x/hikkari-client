<script lang="ts" module>
	export interface Series {
		label: string;
		color: string;
		values: number[];
		/** the line the eye follows first: thicker, with a soft fill under it */
		primary?: boolean;
	}
</script>

<script lang="ts">
	import { bucketText, compact, type Win } from './mock';

	interface Props {
		buckets: number[];
		win: Win;
		series: Series[];
		fmt?: (v: number) => string;
		height?: number;
	}

	let { buckets, win, series, fmt = compact, height = 170 }: Props = $props();

	let width = $state(800);
	let hover = $state<number | null>(null);

	const L = 8;
	const R = 56;
	const T = 12;
	const B = 24;

	const W = $derived(Math.max(240, width));
	const PW = $derived(W - L - R);
	const PH = $derived(height - T - B);
	const n = $derived(buckets.length);

	/** Axis top: the max with headroom, rounded to 1, 2, 2.5 or 5 of a power of ten. */
	const top = $derived.by(() => {
		const raw = Math.max(1, ...series.flatMap((s) => s.values)) * 1.15;
		const pow = 10 ** Math.floor(Math.log10(raw));
		return ([1, 2, 2.5, 5, 10].find((m) => raw <= m * pow) ?? 10) * pow;
	});

	const x = (i: number) => (n < 2 ? L + PW / 2 : L + (i / (n - 1)) * PW);
	const y = (v: number) => T + (1 - v / top) * PH;

	const line = (vs: number[]) => vs.map((v, i) => `${i ? 'L' : 'M'}${x(i).toFixed(1)},${y(v).toFixed(1)}`).join('');
	const area = (vs: number[]) => `${line(vs)}L${x(n - 1)},${y(0)}L${x(0)},${y(0)}Z`;

	const ticks = $derived.by(() => {
		const k = Math.min(6, n);
		return Array.from({ length: k }, (_, j) => Math.round((j * (n - 1)) / Math.max(1, k - 1)));
	});

	function move(e: PointerEvent) {
		const box = (e.currentTarget as HTMLElement).getBoundingClientRect();
		const px = Math.max(L, Math.min(L + PW, e.clientX - box.left));
		hover = n < 2 ? 0 : Math.round(((px - L) / PW) * (n - 1));
	}
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="chart" bind:clientWidth={width} onpointermove={move} onpointerleave={() => (hover = null)}>
	<svg width={W} {height}>
		{#each [0, 0.5, 1] as f (f)}
			<line x1={L} x2={L + PW} y1={y(top * f)} y2={y(top * f)} class="grid" />
			<text x={L + PW + 8} y={y(top * f) + 3}>{fmt(top * f)}</text>
		{/each}
		{#each ticks as i (i)}
			<text x={x(i)} y={height - 6} text-anchor="middle">{bucketText(buckets[i], win).axis}</text>
		{/each}

		{#each series as s (s.label)}
			{#if s.primary}
				<path d={area(s.values)} fill={s.color} opacity="0.08" />
			{/if}
			<path d={line(s.values)} fill="none" stroke={s.color} stroke-width={s.primary ? 1.8 : 1.2} />
		{/each}

		{#if hover !== null}
			<line x1={x(hover)} x2={x(hover)} y1={T} y2={T + PH} class="cursor" />
			{#each series as s (s.label)}
				<circle cx={x(hover)} cy={y(s.values[hover])} r="3" fill={s.color} />
			{/each}
		{/if}
	</svg>

	{#if hover !== null}
		{@const left = x(hover) > L + PW / 2}
		<div class="tip" style="{left ? 'right' : 'left'}: {left ? W - x(hover) + 10 : x(hover) + 10}px">
			<div class="dim">{bucketText(buckets[hover], win).tip}</div>
			{#each series as s (s.label)}
				<div class="row">
					<span><i class="sw" style="background: {s.color}"></i>{s.label}</span>
					<b>{fmt(s.values[hover])}</b>
				</div>
			{/each}
		</div>
	{/if}
</div>

<style>
	.chart {
		position: relative;
		min-width: 0;
	}

	svg {
		display: block;
	}

	text {
		font-family: var(--mono);
		font-size: 10px;
		fill: var(--text-3);
	}

	.grid {
		stroke: var(--border-subtle);
	}

	.cursor {
		stroke: var(--border);
	}

	.tip {
		position: absolute;
		top: 4px;
		min-width: 150px;
		padding: 8px 10px;
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: 6px;
		font-size: 11px;
		pointer-events: none;
	}

	.row {
		display: flex;
		justify-content: space-between;
		gap: 12px;
		color: var(--text-2);
	}

	.row span {
		display: flex;
		align-items: center;
		gap: 6px;
	}
</style>
