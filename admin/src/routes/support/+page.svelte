<script lang="ts">
	import { tickets as seed, ago, type Ticket, type TicketStatus } from '$lib/mock';

	const STATUS: Record<TicketStatus, string> = { open: 'ждёт ответа', answered: 'отвечено', closed: 'закрыто' };

	let list = $state<Ticket[]>(structuredClone(seed));
	let onlyOpen = $state(true);
	let currentId = $state<number | null>(seed[0]?.id ?? null);
	let reply = $state('');

	const shown = $derived(list.filter((t) => !onlyOpen || t.status === 'open'));
	const current = $derived(list.find((t) => t.id === currentId) ?? null);

	function send() {
		if (!current || !reply.trim()) return;
		current.messages.push({ staff: true, text: reply.trim(), ts: Date.now() });
		current.status = 'answered';
		reply = '';
	}
</script>

<div class="head">
	<h1>обращения</h1>
	<div class="chips">
		<button class="chip" class:on={onlyOpen} onclick={() => (onlyOpen = true)}>ждут ответа</button>
		<button class="chip" class:on={!onlyOpen} onclick={() => (onlyOpen = false)}>все</button>
	</div>
</div>

<div class="support">
	<section class="card list">
		{#each shown as t (t.id)}
			<button class="item" class:on={t.id === currentId} onclick={() => (currentId = t.id)}>
				<div class="line">
					<span>{t.from}</span>
					<span class="dim">{ago(t.messages[t.messages.length - 1].ts)}</span>
				</div>
				<div class="dim preview">{t.messages[t.messages.length - 1].text}</div>
			</button>
		{:else}
			<p class="empty">всем ответили</p>
		{/each}
	</section>

	<section class="card thread">
		{#if current}
			<div class="meta">
				<b>#{current.id}</b>
				<span>{current.from}</span>
				<span class="dim">{current.platform} · {current.version}</span>
				<span class:warn={current.status === 'open'} class:dim={current.status !== 'open'}>{STATUS[current.status]}</span>
				{#if current.status !== 'closed'}
					<button class="btn" onclick={() => current && (current.status = 'closed')}>закрыть</button>
				{/if}
			</div>

			<div class="messages">
				{#each current.messages as m, i (i)}
					<div class="msg" class:staff={m.staff}>
						<div>{m.text}</div>
						<div class="dim">{ago(m.ts)}</div>
					</div>
				{/each}
			</div>

			{#if current.status !== 'closed'}
				<div class="reply">
					<textarea
						class="field"
						rows="2"
						placeholder="ответ"
						bind:value={reply}
						onkeydown={(e) => e.key === 'Enter' && (e.metaKey || e.ctrlKey) && send()}
					></textarea>
					<button class="btn primary" onclick={send}>отправить</button>
				</div>
			{/if}
		{:else}
			<p class="empty">выбери обращение</p>
		{/if}
	</section>
</div>

<style>
	.support {
		display: grid;
		grid-template-columns: 300px minmax(0, 1fr);
		gap: 16px;
		align-items: start;
	}

	.list {
		padding: 6px;
	}

	.item {
		display: block;
		width: 100%;
		text-align: left;
		padding: 9px 10px;
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

	.line .dim,
	.preview {
		font-size: 11px;
	}

	.preview {
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.meta {
		display: flex;
		align-items: center;
		gap: 12px;
		padding-bottom: 12px;
		border-bottom: 1px solid var(--border-subtle);
		font-size: 12px;
	}

	.meta .btn {
		margin-left: auto;
	}

	.messages {
		display: flex;
		flex-direction: column;
		gap: 8px;
		padding: 16px 0;
	}

	.msg {
		max-width: 70%;
		padding: 8px 12px;
		background: var(--surface);
		border-radius: 8px;
	}

	.msg.staff {
		align-self: flex-end;
		background: oklch(0.29 0.035 350);
	}

	.msg .dim {
		margin-top: 2px;
		font-size: 10px;
	}

	.reply {
		display: flex;
		gap: 8px;
		align-items: flex-end;
	}

	textarea {
		resize: vertical;
	}
</style>
