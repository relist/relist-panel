import { writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import react from '@vitejs/plugin-react';
import { defineConfig, type Plugin } from 'vite';

const outDir = fileURLToPath(new URL('../internal/webassets/dist', import.meta.url));

// dist is git-ignored except dist/.gitkeep, which lets the Go package compile
// before any frontend build. emptyOutDir deletes it, so put it back.
function keepGitkeep(): Plugin {
  return {
    name: 'relist-keep-gitkeep',
    apply: 'build',
    closeBundle() {
      writeFileSync(`${outDir}/.gitkeep`, '');
    },
  };
}

export default defineConfig({
  plugins: [react(), keepGitkeep()],
  build: {
    outDir,
    emptyOutDir: true,
  },
});
