import { DiffEditor } from '@monaco-editor/react';
import { useContext } from 'react';
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
  return (
    <DiffEditor
      height={height}
      language="yaml"
      loading={<span>Loading editor…</span>}
      modified={modified}
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
