import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [
		tailwindcss(),
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},

			// Static SPA embedded in the Go binary; unknown paths fall back to index.html.
			adapter: adapter({ fallback: 'index.html' })
		})
	],
	server: {
		// `make dev`: the Go backend runs on :8080.
		proxy: {
			'/api': 'http://localhost:8080'
		}
	}
});
