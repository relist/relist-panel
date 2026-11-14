import Editor from '@monaco-editor/react';
import type { editor as MonacoEditorApi } from 'monaco-editor';
import { useContext, useState } from 'react';
import { CODE_FONT_STACK, MONACO_THEME, ThemeModeContext } from '../app/theme';
import { EditorPending, useMonaco } from './MonacoGate';

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
 */
export function YamlEditor({
  autoHeight,
  height = '100%',
  keepCurrentModel,
  onChange,
  onMount,
  path,
  readOnly,
  value,
}: {
  /** Size the editor to its content, clamped to `[min, max]` (a CSS length). Overrides `height`. */
  autoHeight?: { max: string; min: number };
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
  const [contentHeight, setContentHeight] = useState(0);
  const resolvedHeight = autoHeight ? `clamp(${autoHeight.min}px, ${contentHeight}px, ${autoHeight.max})` : height;
  const monaco = useMonaco();

  if (monaco !== 'ready') return <EditorPending height={resolvedHeight} status={monaco} />;
  return (
    <Editor
      height={resolvedHeight}
      keepCurrentModel={keepCurrentModel}
      language="yaml"
      loading={<span>Loading editor…</span>}
      onChange={(next) => onChange?.(next ?? '')}
      onMount={(editor) => {
        if (autoHeight) {
          setContentHeight(editor.getContentHeight());
          editor.onDidContentSizeChange((e) => setContentHeight(e.contentHeight));
        }
        onMount?.(editor);
      }}
      options={{
        ...MONACO_OPTIONS,
        ...(autoHeight && { automaticLayout: true, scrollbar: { alwaysConsumeMouseWheel: false } }),
        readOnly,
      }}
      path={path}
      theme={MONACO_THEME[mode]}
      value={value}
    />
  );
}
