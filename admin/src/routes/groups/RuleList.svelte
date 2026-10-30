<script lang="ts">
	interface Props {
		label: string;
		hint: string;
		items: string[];
	}

	let { label, hint, items = $bindable() }: Props = $props();
	let input = $state('');

	/** Adds every value in text, split on newlines, commas and spaces, skipping ones already there. */
	function add(text: string) {
		const have = new Set(items.map((x) => x.toLowerCase()));
		const fresh = text
			.split(/[\s,]+/)
			.map((x) => x.trim())
			.filter((x) => x && !have.has(x.toLowerCase()) && have.add(x.toLowerCase()));
		if (fresh.length) items = [...items, ...fresh];
		input = '';
	}

	function paste(e: ClipboardEvent) {
		const text = e.clipboardData?.getData('text') ?? '';
		if (!/[\n,]/.test(text)) return;
		e.preventDefault();
		add(text);
	}
</script>

<div class="list">
	<div class="label">{label} · {items.length}</div>
	{#each items as x (x)}
		<div class="item">
			<span>{x}</span>
			<button onclick={() => (items = items.filter((y) => y !== x))} title="убрать">×</button>
		</div>
	{/each}
	<input
		class="field mono"
		placeholder={hint}
		bind:value={input}
		onkeydown={(e) => e.key === 'Enter' && add(input)}
		onpaste={paste}
	/>
</div>

<style>
	.list {
		min-width: 0;
	}

	.label {
		margin-bottom: 8px;
		font-size: 11px;
		color: var(--text-3);
	}

	.item {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
		padding: 2px 0;
		font-family: var(--mono);
		font-size: 12px;
		color: var(--text-2);
	}

	.item span {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.item button {
		visibility: hidden;
		padding: 0 4px;
		color: var(--text-3);
	}

	.item:hover button {
		visibility: visible;
	}

	.item button:hover {
		color: var(--bad);
	}

	.field {
		margin-top: 6px;
	}
</style>
