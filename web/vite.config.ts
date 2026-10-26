import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';
import { sveltekit } from '@sveltejs/kit/vite';
import adapter from '@sveltejs/adapter-node';
const target = process.env.API_ORIGIN || 'http://127.0.0.1:8080';
export default defineConfig({
  plugins: [sveltekit({ adapter: adapter() }), {
    name: 'local-checkout-info',
    apply: 'serve',
    configureServer(server) {
      const checkout = fileURLToPath(new URL('..', import.meta.url));
      const git = (...args: string[]) => {
        try { return execFileSync('git', args, { cwd: checkout, encoding: 'utf8' }).trim(); }
        catch { return 'unknown'; }
      };
      server.middlewares.use('/__dev/info', (_req, res) => {
        res.setHeader('Content-Type', 'application/json; charset=utf-8');
        res.setHeader('Cache-Control', 'no-store');
        res.end(JSON.stringify({ checkout, revision: git('rev-parse', '--short', 'HEAD'),
          branch: git('branch', '--show-current'), dirty: !!git('status', '--porcelain'), api: target }));
      });
      server.httpServer?.once('listening', () => {
        server.config.logger.info(`Local checkout: ${checkout}\nRevision: ${git('rev-parse', '--short', 'HEAD')} · API: ${target}\nDetails: /__dev/info`);
      });
    }
  }],
  server: {
    proxy: Object.fromEntries(
      ['/api', '/healthz', '/readyz'].map((path) => [
        path,
        { target, ws: true, changeOrigin: false }
      ])
    )
  }
});
