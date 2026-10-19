import path from 'node:path';
import { defineConfig } from 'vitest/config';
import { localeBundles } from './scripts/locale-bundles';

export default defineConfig({
  plugins: [localeBundles(import.meta.dirname)],
  // Same alias as vite.config.ts, so modules that import through it load in
  // unit tests too.
  resolve: { alias: { '@': path.resolve(import.meta.dirname, './src') } },
  test: {
    include: ['src/**/*.{test,spec}.{js,jsx,ts,tsx}'],
    exclude: ['node_modules/**', 'dist/**', 'tests/smoke/**'],
  },
});
