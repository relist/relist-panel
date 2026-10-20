import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// RELIST_PANEL_URL points the dev server's API at a running panel, as
// `task dev:migration` does for each sandbox panel.
const panel = process.env.RELIST_PANEL_URL;

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/webassets/dist',
    emptyOutDir: true,
  },
  server: panel
    ? {
        proxy: {
          '/api': { target: panel, ws: true },
          '/healthz': panel,
        },
      }
    : undefined,
});
