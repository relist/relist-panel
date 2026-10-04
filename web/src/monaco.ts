import { loader } from '@monaco-editor/react';
import * as monaco from 'monaco-editor/esm/vs/editor/editor.api.js';
import 'monaco-editor/esm/vs/basic-languages/yaml/yaml.contribution.js';
import EditorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker';
import { configureMonacoYaml } from 'monaco-yaml';
// Wrapping the worker import in a local file is the documented Vite workaround: importing
// monaco-yaml/yaml.worker?worker directly fails to boot, and Monaco silently falls back to the
// main-thread editor worker, which has no YAML handlers.
import YamlWorker from './yaml.worker.ts?worker';
// Pinned from https://raw.githubusercontent.com/compose-spec/compose-go/v2.15.0/schema/compose-spec.json
// (the schema listed for docker-compose.yml in the SchemaStore catalog). Bundled inline so the
// editor's completion, hover and validation work fully offline.
import composeSchema from './schemas/compose-spec.json';
import { MONACO_THEME, monacoThemeData } from './app/theme';

const createWorkerFor = (_workerID: string, label: string) => {
  if (label === 'yaml') {
    return new YamlWorker();
  }
  return new EditorWorker();
};

self.MonacoEnvironment = {
  getWorker: createWorkerFor,
};

// monaco-editor 0.55 changed `editor.createWebWorker` to require an already-created worker
// (`opts.worker`), while monaco-yaml still calls it the old way with `{label, createData}`.
// Left as is, the call throws and Monaco silently falls back to the main-thread editor worker.
// This bridge mirrors the upstream shim in monaco-editor/esm/vs/common/workers.js, which is only
// reachable when importing the full editor.main bundle.
const createWebWorkerNewStyle = monaco.editor.createWebWorker.bind(monaco.editor);
monaco.editor.createWebWorker = ((opts: { label?: string; createData?: unknown; host?: unknown; keepIdleModels?: boolean }) => {
  const worker = Promise.resolve(createWorkerFor('workerMain.js', opts.label ?? '')).then((w) => {
    w.postMessage('ignore');
    w.postMessage(opts.createData);
    return w;
  });
  return createWebWorkerNewStyle({ worker, host: opts.host, keepIdleModels: opts.keepIdleModels } as Parameters<typeof createWebWorkerNewStyle>[0]);
}) as typeof monaco.editor.createWebWorker;

loader.config({ monaco });

// Monaco ships `vs` / `vs-dark`; this theme is the same editor wearing the
// panel's design tokens (see `app/theme.ts`). Dark mode is out of scope
// (ADR-0004), so only the light theme is defined.
monaco.editor.defineTheme(MONACO_THEME.light, monacoThemeData('light'));

configureMonacoYaml(monaco, {
  enableSchemaRequest: false,
  hover: true,
  completion: true,
  validate: true,
  schemas: [
    {
      uri: 'https://raw.githubusercontent.com/compose-spec/compose-go/v2.15.0/schema/compose-spec.json',
      fileMatch: ['*'],
      schema: composeSchema,
    },
  ],
});
