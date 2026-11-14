// @vitest-environment jsdom
import '../test/dom-setup';
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

let resolveMonaco!: () => void;
const monacoLoaded = new Promise<void>((resolve) => {
  resolveMonaco = resolve;
});
const imports = vi.fn();
vi.mock('../monaco', async () => {
  imports();
  await monacoLoaded;
  return {};
});
vi.mock('@monaco-editor/react', () => ({
  default: (p: { path: string }) => <div>editor {p.path}</div>,
}));

const { YamlEditor } = await import('./YamlEditor');

describe('YamlEditor', () => {
  it('waits for Monaco, loading it once for every editor', async () => {
    render(
      <>
        <YamlEditor path="a.yml" value="" />
        <YamlEditor path="b.yml" value="" />
      </>,
    );
    expect(screen.getAllByText('Loading editor…')).toHaveLength(2);
    expect(screen.queryByText(/^editor /)).toBeNull();

    resolveMonaco();
    expect(await screen.findByText('editor a.yml')).toBeTruthy();
    expect(screen.getByText('editor b.yml')).toBeTruthy();
    expect(imports).toHaveBeenCalledTimes(1);
  });
});
