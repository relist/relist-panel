import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5273,
    // The single-file build output lives inside the project root; watching it
    // would reload the dev server on every build.
    watch: { ignored: ['**/dist-single/**'] },
  },
});
