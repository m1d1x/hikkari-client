<script lang="ts">
	import { WINDOWS, type Win } from './mock';

	let { value = $bindable() }: { value: Win } = $props();
</script>

<div class="chips">
	{#each WINDOWS as w (w)}
		<button class="chip" class:on={value === w} onclick={() => (value = w)}>{w}</button>
	{/each}
</div>
