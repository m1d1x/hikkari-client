<script lang="ts">
	import '../app.css';
	import { page } from '$app/state';

	let { children } = $props();

	const NAV = [
		{ href: '/', label: 'обзор' },
		{ href: '/errors', label: 'ошибки' },
		{ href: '/support', label: 'обращения' },
		{ href: '/donations', label: 'донаты' },
		{ href: '/presets', label: 'пресеты' },
		{ href: '/groups', label: 'группы' },
	];

	const active = (href: string) =>
		href === '/' ? page.url.pathname === '/' : page.url.pathname.startsWith(href);
</script>

<div class="app">
	<aside>
		<div class="logo">hikkari <span>admin</span></div>
		<nav>
			{#each NAV as item (item.href)}
				<a href={item.href} class:on={active(item.href)}>{item.label}</a>
			{/each}
		</nav>
	</aside>
	<main>{@render children()}</main>
</div>

<style>
	.app {
		display: flex;
		height: 100vh;
	}

	aside {
		width: 180px;
		flex-shrink: 0;
		background: var(--side);
		border-right: 1px solid var(--border-subtle);
		padding: 16px 8px;
	}

	.logo {
		padding: 0 12px 16px;
		font-weight: 600;
		letter-spacing: 0.02em;
	}

	.logo span {
		margin-left: 4px;
		font-size: 10px;
		color: var(--primary);
	}

	nav {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	nav a {
		position: relative;
		padding: 7px 12px;
		border-radius: 6px;
		color: var(--text-2);
	}

	nav a:hover {
		color: var(--text);
		background: var(--surface);
	}

	nav a.on {
		color: var(--text);
		background: var(--surface);
	}

	nav a.on::before {
		content: '';
		position: absolute;
		left: 0;
		top: 7px;
		bottom: 7px;
		width: 2px;
		border-radius: 0 2px 2px 0;
		background: var(--primary);
	}

	main {
		flex: 1;
		min-width: 0;
		overflow-y: auto;
		padding: 24px;
	}
</style>
