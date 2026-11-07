import { DiffEditor, type DiffOnMount } from '@monaco-editor/react';
import { useContext, useEffect, useRef } from 'react';
import { CODE_FONT_STACK, MONACO_THEME, ThemeModeContext } from '../app/theme';
import '../monaco';

/** A read-only side-by-side diff of two text files (Monaco). */
export function DiffViewer({
  height = 360,
  modified,
  original,
}: {
  height?: number;
  modified: string;
  original: string;
}) {
  const mode = useContext(ThemeModeContext);
  // @monaco-editor/react disposes the diff's models before the diff editor on
  // unmount, which Monaco 0.55 reports as "TextModel got disposed before
  // DiffEditorWidget model got reset". Keep them through its unmount, then
  // dispose them once the editor is gone.
  const models = useRef<{ dispose(): void }[]>([]);
  const onMount: DiffOnMount = (editor) => {
    const { modified: m, original: o } = editor.getModel() ?? {};
    models.current = [o, m].filter((model) => model !== undefined);
  };
  useEffect(
    () => () => {
      const kept = models.current;
      setTimeout(() => kept.forEach((model) => model.dispose()), 0);
    },
    [],
  );
  return (
    <DiffEditor
      height={height}
      keepCurrentModifiedModel
      keepCurrentOriginalModel
      language="yaml"
      loading={<span>Loading editor…</span>}
      modified={modified}
      onMount={onMount}
      options={{
        readOnly: true,
        originalEditable: false,
        renderSideBySide: true,
        minimap: { enabled: false },
        fontSize: 13,
        fontFamily: CODE_FONT_STACK,
        scrollBeyondLastLine: false,
        automaticLayout: true,
        scrollbar: { alwaysConsumeMouseWheel: false },
      }}
      original={original}
      theme={MONACO_THEME[mode]}
    />
  );
}
