// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ComposePreviewModal } from './ComposePreviewModal';

const panel = vi.hoisted(() => ({ previewCompose: vi.fn() }));
vi.mock('../data/store', () => ({ usePanel: () => panel }));

// Monaco cannot run in jsdom; the stand-in shows what the editor would get.
vi.mock('./YamlEditor', () => ({
  YamlEditor: ({ path, readOnly, value }: { path: string; readOnly?: boolean; value: string }) => (
    <pre data-path={path} data-readonly={String(Boolean(readOnly))} data-testid="preview-editor">
      {value}
    </pre>
  ),
}));

const RESOLVED = 'services:\n  web:\n    networks:\n      - relist-network\n';

function renderModal(props: { dirty?: boolean; draft?: string; onClose?: () => void; open?: boolean } = {}) {
  return render(
    <ComposePreviewModal
      composeId={7}
      dirty={props.dirty ?? false}
      draft={props.draft ?? 'services: {web: {image: nginx}}'}
      editorPath="1-2-7"
      onClose={props.onClose ?? (() => undefined)}
      open={props.open ?? true}
    />,
  );
}

describe('ComposePreviewModal', () => {
  beforeEach(() => {
    panel.previewCompose.mockReset();
  });

  it('previews the draft in a read-only editor with its own model', async () => {
    panel.previewCompose.mockResolvedValue({ warnings: [], yaml: RESOLVED });
    renderModal({ draft: 'services: {api: {image: busybox}}' });

    const editor = await screen.findByTestId('preview-editor');
    expect(panel.previewCompose).toHaveBeenCalledWith(7, 'services: {api: {image: busybox}}');
    expect(editor.textContent).toBe(RESOLVED);
    expect(editor.dataset.readonly).toBe('true');
    expect(editor.dataset.path).not.toBe('1-2-7.yml');
    expect(screen.queryByText('Includes your unsaved changes.')).toBeNull();
  });

  it('says when the preview includes unsaved changes', async () => {
    panel.previewCompose.mockResolvedValue({ warnings: [], yaml: RESOLVED });
    renderModal({ dirty: true });
    expect(await screen.findByText('Includes your unsaved changes.')).toBeTruthy();
  });

  it('shows each deploy warning', async () => {
    panel.previewCompose.mockResolvedValue({
      warnings: ['service web: traefik.enable=false was overridden'],
      yaml: RESOLVED,
    });
    renderModal();
    expect(await screen.findByText('service web: traefik.enable=false was overridden')).toBeTruthy();
  });

  it('shows a refusal inline without an editor', async () => {
    panel.previewCompose.mockRejectedValue(
      new Error('domain api.example.com/ points at service "web", which is not in the compose file'),
    );
    renderModal();
    expect(
      await screen.findByText('domain api.example.com/ points at service "web", which is not in the compose file'),
    ).toBeTruthy();
    expect(screen.queryByTestId('preview-editor')).toBeNull();
  });

  it('does not load while closed, and Close calls onClose', async () => {
    const { rerender } = renderModal({ open: false });
    expect(panel.previewCompose).not.toHaveBeenCalled();

    panel.previewCompose.mockResolvedValue({ warnings: [], yaml: RESOLVED });
    const onClose = vi.fn();
    rerender(
      <ComposePreviewModal composeId={7} dirty={false} draft="x" editorPath="1-2-7" onClose={onClose} open />,
    );
    await screen.findByTestId('preview-editor');
    // The footer button, not the title bar's close icon (also named Close).
    const footerClose = screen.getByText('Close').closest('button');
    expect(footerClose).toBeTruthy();
    await userEvent.click(footerClose as HTMLButtonElement);
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
  });
});
