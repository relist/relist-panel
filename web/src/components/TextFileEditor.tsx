import Editor from '@monaco-editor/react';
import type { editor as MonacoEditorApi } from 'monaco-editor';
import { useContext } from 'react';
import { MONACO_THEME, ThemeModeContext } from '../app/theme';
import { MONACO_OPTIONS } from './YamlEditor';
import '../monaco';

/**
 * The Volume Explorer's file editor.
 *
 * The same Monaco setup as the Compose YAML editor — the panel ships one
 * editor, not two — with two differences that matter: the language comes from
 * the filename instead of being pinned to yaml, and the model is keyed under a
 * `volume:` namespace, so a file from a Volume can never share a model (or its
 * undo history) with a Compose's `docker-compose.yml`.
 */

const LANGUAGE_BY_EXTENSION: Record<string, string> = {
  conf: 'ini',
  csv: 'plaintext',
  env: 'ini',
  json: 'json',
  log: 'plaintext',
  md: 'markdown',
  sh: 'shell',
  sql: 'sql',
  toml: 'ini',
  ts: 'typescript',
  txt: 'plaintext',
  xml: 'xml',
  yaml: 'yaml',
  yml: 'yaml',
};

function languageFor(path: string): string {
  const name = path.split('/').filter(Boolean).pop() ?? '';
  const extension = name.includes('.') ? name.split('.').pop()!.toLowerCase() : '';
  return LANGUAGE_BY_EXTENSION[extension] ?? 'plaintext';
}

export function TextFileEditor({
  height,
  onChange,
  onMount,
  path,
  readOnly,
  value,
}: {
  height?: number | string;
  onChange?: (value: string) => void;
  onMount?: (editor: MonacoEditorApi.IStandaloneCodeEditor) => void;
  path: string;
  readOnly?: boolean;
  value: string;
}) {
  // Monaco is themed separately from antd, so it takes the mode from context
  // rather than from a token (see `monacoThemeData` in app/theme.ts).
  const mode = useContext(ThemeModeContext);

  return (
    <Editor
      height={height ?? '100%'}
      language={languageFor(path)}
      loading={<span>Loading editor…</span>}
      onChange={(next) => onChange?.(next ?? '')}
      onMount={onMount}
      // Keyed by the file, so switching files swaps models rather than leaving
      // one file's undo history attached to another's path.
      path={`volume:${path}`}
      options={{ ...MONACO_OPTIONS, readOnly }}
      theme={MONACO_THEME[mode]}
      value={value}
    />
  );
}
