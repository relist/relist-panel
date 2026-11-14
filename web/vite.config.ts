import { writeFileSync } from 'node:fs';
import { readdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { brotliCompress, constants as zlib, gzip } from 'node:zlib';
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

const brotli = promisify(brotliCompress);
const gz = promisify(gzip);

// Writes a .br and a .gz next to every compressible file in assets/. The panel
// runs on a quarter CPU, so it sends these as they are instead of compressing
// per request (internal/webassets). A sibling that saves nothing is skipped.
function precompress(): Plugin {
  return {
    name: 'relist-precompress',
    apply: 'build',
    async closeBundle() {
      const dir = join(outDir, 'assets');
      const names = (await readdir(dir)).filter((n) => /\.(js|mjs|css|svg|json|ttf|wasm)$/.test(n));
      await Promise.all(
        names.map(async (name) => {
          const file = join(dir, name);
          const data = await readFile(file);
          if (data.length < 1024) return;
          const [br, gzipped] = await Promise.all([
            brotli(data, {
              params: {
                [zlib.BROTLI_PARAM_QUALITY]: zlib.BROTLI_MAX_QUALITY,
                [zlib.BROTLI_PARAM_SIZE_HINT]: data.length,
              },
            }),
            gz(data, { level: zlib.Z_BEST_COMPRESSION }),
          ]);
          if (br.length < data.length) await writeFile(`${file}.br`, br);
          if (gzipped.length < data.length) await writeFile(`${file}.gz`, gzipped);
        }),
      );
    },
  };
}

export default defineConfig({
  plugins: [react(), keepGitkeep(), precompress()],
  build: {
    outDir,
    emptyOutDir: true,
  },
});
