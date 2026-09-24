import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';
import { viteSingleFile } from 'vite-plugin-singlefile';

/**
 * Single-file build: bundles the whole prototype (JS, CSS, the compose schema)
 * into one self-contained HTML file, so it can be opened from disk or attached
 * to a review without a dev server. Monaco's YAML worker is not inlined, so the
 * schema validation is inert in this build — layout and interaction are intact.
 */
export default defineConfig({
  plugins: [react(), viteSingleFile()],
  build: {
    outDir: 'dist-single',
    assetsInlineLimit: 100_000_000,
    cssCodeSplit: false,
    emptyOutDir: true,
  },
});
