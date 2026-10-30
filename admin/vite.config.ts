import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [sveltekit()],
	// The engine's preset catalog is read from the repo, one level up.
	server: { port: 5175, fs: { allow: ['..'] } },
});
