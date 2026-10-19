import path from 'node:path';
import { fileURLToPath } from 'node:url';
import react from '@vitejs/plugin-react';
import { visualizer } from 'rollup-plugin-visualizer';
import { defineConfig } from 'vite';
import { loadBuildExtensions } from './scripts/extensions.mjs';
import { localeBundles } from './scripts/locale-bundles';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const docsDevTarget =
  process.env.LAZU_DOCS_DEV_TARGET || 'http://127.0.0.1:3002';
// Backend target for the dev proxy. Overridable so a second (worktree) stack
// can run against an isolated API port without colliding with the default one.
const apiDevTarget = process.env.LAZU_API_DEV_TARGET || 'http://localhost:3000';

const buildExtensions = await loadBuildExtensions(__dirname);

// https://vitejs.dev/config/
export default defineConfig({
  envPrefix: ['VITE_', 'PUBLIC_'],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  plugins: [
    react(),
    localeBundles(__dirname),
    // Emit a treemap to `dist/stats.html` so we can audit why
    // vchart-simple, mermaid.core, etc. are large without guessing.
    // The visualizer plugin only runs during `vite build`; `vite dev`
    // is unaffected because no `bundle` Rollup hook fires there.
    // `dist/stats.html` is gitignored — open it locally after a build
    // with `open apps/web/dist/stats.html` or sample it in CI later.
    visualizer({
      filename: 'dist/stats.html',
      template: 'treemap',
      gzipSize: true,
      brotliSize: true,
      sourcemap: false,
    }),
  ],
  optimizeDeps: {
    esbuildOptions: {
      loader: {
        '.json': 'json',
      },
    },
  },
  build: {
    manifest: true,
    // Compress the production bundle within its gzip budget while retaining
    // Vite's browser targets and Terser's default safe transforms.
    minify: 'terser',
    rollupOptions: {
      // The SPA, plus the stylesheet/behaviour bundle for the pages the API
      // renders (see src/public-site/entry.ts).
      input: {
        main: path.resolve(__dirname, 'index.html'),
        site: path.resolve(__dirname, 'src/public-site/entry.ts'),
      },
    },
    // Let Rollup follow dynamic-import boundaries. Manual vendor chunks caused
    // circular dependencies that promoted Markdown and Mermaid into index.html.
    // VChart stays route-lazy and is gated by its gzip budget; its minified
    // source is ~1.26 MB, so keep Vite's raw-size warning above that boundary.
    chunkSizeWarningLimit: 1300,
  },
  server: {
    host: '0.0.0.0',
    proxy: {
      '/models.json': {
        target: apiDevTarget,
        changeOrigin: true,
      },
      // API-rendered public pages. In dev their SSI includes stay
      // unresolved; the docker stack (nginx) serves them complete.
      '^/(zh/|zh-TW/|ja/)?models(/|\\.|$)': {
        target: apiDevTarget,
        changeOrigin: true,
      },
      // API-rendered pages of extensions (their build.mjs devApiPaths).
      ...Object.fromEntries(
        buildExtensions
          .flatMap((extension) => extension.devApiPaths ?? [])
          .map((pattern: string) => [
            pattern,
            { target: apiDevTarget, changeOrigin: true },
          ]),
      ),
      '/api/search/backends': {
        target: apiDevTarget,
        changeOrigin: true,
      },
      '/api/search': {
        target: docsDevTarget,
        changeOrigin: true,
      },
      '/api': {
        target: apiDevTarget,
        changeOrigin: true,
      },
      '/mj': {
        target: apiDevTarget,
        changeOrigin: true,
      },
      '/pg': {
        target: apiDevTarget,
        changeOrigin: true,
      },
      '/v1': {
        target: apiDevTarget,
        changeOrigin: true,
      },
      '/setup': {
        target: apiDevTarget,
        changeOrigin: true,
      },
      '/doc': {
        target: docsDevTarget,
        changeOrigin: true,
      },
      '/docs': {
        target: docsDevTarget,
        changeOrigin: true,
      },
      '/_next': {
        target: docsDevTarget,
        changeOrigin: true,
      },
    },
  },
});
