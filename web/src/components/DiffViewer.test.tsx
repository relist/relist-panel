// @vitest-environment jsdom
import '../test/dom-setup';
import { render } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

// Records the order in which the diff's models and the editor are disposed.
const order: string[] = [];
const props: Record<string, unknown> = {};

vi.mock('../monaco', () => ({}));
vi.mock('@monaco-editor/react', async () => {
  const React = await import('react');
  return {
    DiffEditor: (p: Record<string, unknown> & { onMount?: (editor: unknown) => void }) => {
      Object.assign(props, p);
      React.useEffect(() => {
        const original = { dispose: () => order.push('original') };
        const modified = { dispose: () => order.push('modified') };
        p.onMount?.({ getModel: () => ({ modified, original }) });
        // What the library does on unmount: dispose the models unless told
        // to keep them, then the editor.
        return () => {
          if (!p.keepCurrentOriginalModel) original.dispose();
          if (!p.keepCurrentModifiedModel) modified.dispose();
          order.push('editor');
        };
        // eslint-disable-next-line react-hooks/exhaustive-deps
      }, []);
      return null;
    },
  };
});

const { DiffViewer } = await import('./DiffViewer');

afterEach(() => {
  order.length = 0;
});

describe('DiffViewer', () => {
  // Monaco 0.55 throws "TextModel got disposed before DiffEditorWidget model
  // got reset" when the models go before the diff editor.
  it('disposes its models only after the diff editor', async () => {
    const view = render(<DiffViewer modified="b" original="a" />);
    view.unmount();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(order).toEqual(['editor', 'original', 'modified']);
    expect(props.keepCurrentOriginalModel).toBe(true);
  });
});
