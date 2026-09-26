import Editor from '@monaco-editor/react';
import type { editor as MonacoEditorApi } from 'monaco-editor';
import { useContext } from 'react';
import { CODE_FONT_STACK, MONACO_THEME, ThemeModeContext } from '../app/theme';
import { useSceneModelPrefix } from '../scenes/scope';
import '../monaco';

export const MONACO_OPTIONS = {
  minimap: { enabled: false },
  fontSize: 13,
  fontFamily: CODE_FONT_STACK,
  lineHeight: 20,
  scrollBeyondLastLine: false,
  renderLineHighlight: 'gutter',
  tabSize: 2,
  smoothScrolling: true,
  padding: { bottom: 16, top: 16 },
} as const;

/**
 * The one editor surface in the panel. `path` keeps a separate Monaco model per
 * Compose, so undo history and cursor position survive navigating away and back.
 *
 * Two editors mounted with the same `path` share that one model — which is how
 * the full screen editor and the editor on the page stay the same document. The
 * second one must pass `keepCurrentModel`: without it, unmounting disposes the
 * model the first editor is still showing.
 *
 * Inside a scene scope the path carries the frame's prefix (`scene:<id>:`),
 * because models live in one registry per document: two frames of one screen
 * must never share a model (ticket 10), while within a frame the shared path —
 * prefix included — keeps the deliberate sharing above working.
 */
export function YamlEditor({
  height = '100%',
  keepCurrentModel,
  onChange,
  onMount,
  path,
  readOnly,
  value,
}: {
  height?: number | string;
  keepCurrentModel?: boolean;
  onChange?: (value: string) => void;
  onMount?: (editor: MonacoEditorApi.IStandaloneCodeEditor) => void;
  path: string;
  readOnly?: boolean;
  value: string;
}) {
  // Monaco is themed separately from antd, so it takes the mode from context
  // rather than from a token (see `monacoThemeData` in app/theme.ts).
  const mode = useContext(ThemeModeContext);
  const modelPrefix = useSceneModelPrefix();

  return (
    <Editor
      height={height}
      keepCurrentModel={keepCurrentModel}
      language="yaml"
      loading={<span>Loading editor…</span>}
      onChange={(next) => onChange?.(next ?? '')}
      onMount={(editor) => onMount?.(editor)}
      options={{ ...MONACO_OPTIONS, readOnly }}
      path={`${modelPrefix}${path}`}
      theme={MONACO_THEME[mode]}
      value={value}
    />
  );
}
