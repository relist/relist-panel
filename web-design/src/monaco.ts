import { loader } from '@monaco-editor/react';
import * as monaco from 'monaco-editor/esm/vs/editor/editor.api.js';
import 'monaco-editor/esm/vs/basic-languages/yaml/yaml.contribution.js';
import EditorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker';
import { configureMonacoYaml } from 'monaco-yaml';
// Wrapping the worker import in a local file is the documented Vite workaround:
// importing monaco-yaml/yaml.worker?worker directly fails to boot, and Monaco
// silently falls back to the main-thread editor worker, which has no YAML handlers.
import YamlWorker from './yaml.worker.ts?worker';
// Pinned from the same compose-go schema the production panel bundles, so the
// design prototype validates YAML exactly like the real editor.
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

// monaco-editor 0.55 changed `editor.createWebWorker` to require an
// already-created worker (`opts.worker`), while monaco-yaml still calls it the
// old way with `{label, createData}`. Left as is, the call throws and Monaco
// silently falls back to the main-thread editor worker. This bridge mirrors the
// upstream shim in monaco-editor/esm/vs/common/workers.js.
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

// Monaco ships `vs` / `vs-dark`; these two are the same editor wearing the
// panel's tokens, so the editor follows the theme switch like every other
// surface. Defined once here, selected per mode in `YamlEditor`.
for (const mode of ['light', 'dark'] as const) {
  monaco.editor.defineTheme(MONACO_THEME[mode], monacoThemeData(mode));
}

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
