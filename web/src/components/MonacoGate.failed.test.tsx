// @vitest-environment jsdom
import '../test/dom-setup';
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../monaco', () => {
  throw new Error('Failed to fetch dynamically imported module');
});
vi.mock('@monaco-editor/react', () => ({ default: () => <div>editor</div> }));

const { YamlEditor } = await import('./YamlEditor');

describe('YamlEditor when Monaco cannot load', () => {
  // After a Panel update the old chunk is gone and the browser will not retry
  // a failed module, so the editor asks for a reload instead of spinning.
  it('offers a reload', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    render(<YamlEditor path="a.yml" value="" />);
    expect(await screen.findByText('The editor could not load')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Reload' })).toBeTruthy();
    expect(screen.queryByText('editor')).toBeNull();
  });
});
