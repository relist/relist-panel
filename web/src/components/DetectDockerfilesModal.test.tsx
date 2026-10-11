// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import type { DetectedDockerfile } from '../data/types';
import { DetectDockerfilesModal } from './DetectDockerfilesModal';

const items: DetectedDockerfile[] = [
  { context: './src/api', dockerfile: 'Dockerfile', path: 'api/Dockerfile', service: 'api' },
  { context: './src/web', dockerfile: 'Dockerfile', path: 'web/Dockerfile', service: 'web' },
];

function renderDialog(onInsert = vi.fn()) {
  render(<DetectDockerfilesModal existingServices={['web']} items={items} onClose={() => undefined} onInsert={onInsert} open />);
  return onInsert;
}

describe('DetectDockerfilesModal', () => {
  it('pre-checks every row', () => {
    renderDialog();
    const boxes = screen.getAllByRole('checkbox', { name: /^Use / });
    expect(boxes).toHaveLength(2);
    expect(boxes.every((b) => (b as HTMLInputElement).checked)).toBe(true);
  });

  it('offers "replace image" only for Services already in the YAML', () => {
    renderDialog();
    const rows = screen.getAllByRole('row');
    expect(within(rows[1]).queryByText('replace image')).toBeNull();
    expect(within(rows[2]).getByText('replace image')).toBeTruthy();
  });

  it('inserts the edited, checked targets', async () => {
    const user = userEvent.setup();
    const onInsert = renderDialog();

    const service = screen.getByLabelText('Service for api/Dockerfile');
    await user.clear(service);
    await user.type(service, 'backend');
    const context = screen.getByLabelText('Context for api/Dockerfile');
    await user.clear(context);
    await user.type(context, './src');
    await user.click(screen.getByRole('checkbox', { name: 'Use web/Dockerfile' }));
    await user.click(screen.getByRole('button', { name: 'Insert into editor' }));

    expect(onInsert).toHaveBeenCalledWith([
      { context: './src', dockerfile: 'Dockerfile', replaceImage: false, service: 'backend' },
    ]);
  });
});
