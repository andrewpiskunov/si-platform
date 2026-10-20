import { defineConfig } from 'vite';
import { sveltekit } from '@sveltejs/kit/vite';
import adapter from '@sveltejs/adapter-node';
const target = process.env.API_ORIGIN || 'http://127.0.0.1:8080';
export default defineConfig({
  plugins: [sveltekit({ adapter: adapter() })],
  server: {
    proxy: Object.fromEntries(
      ['/api', '/healthz', '/readyz'].map((path) => [
        path,
        { target, ws: true, changeOrigin: false }
      ])
    )
  }
});
