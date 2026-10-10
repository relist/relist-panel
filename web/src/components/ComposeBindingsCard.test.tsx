// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Compose, Environment, VariableSet } from '../data/types';
import { ComposeBindingsCard } from './ComposeBindingsCard';

const panel = vi.hoisted(() => ({
  isPending: vi.fn(() => false),
  loadBindings: vi.fn(),
  loadDomains: vi.fn(),
  loadEffectiveVariables: vi.fn(),
  loadVariableSets: vi.fn(),
  saveBindings: vi.fn(),
}));

vi.mock('../data/store', () => ({ usePanel: () => panel }));

const compose = { id: 7, name: 'shop' } as Compose;
const environment = { id: 1, name: 'production' } as Environment;
const set = (id: number, name: string): VariableSet => ({ createdAt: '', entries: [], environmentId: 1, id, name, updatedAt: '', usedBy: [] });

function renderCard(onError = vi.fn()) {
  render(
    <App>
      <ComposeBindingsCard compose={compose} environment={environment} onError={onError} />
    </App>,
  );
  return { onError };
}

describe('ComposeBindingsCard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    panel.isPending.mockReturnValue(false);
    panel.loadVariableSets.mockResolvedValue([set(1, 'db'), set(2, 'cache')]);
    panel.loadBindings.mockResolvedValue([]);
    panel.loadDomains.mockResolvedValue({ domains: [], services: ['web', 'worker'] });
    panel.saveBindings.mockResolvedValue(undefined);
  });

  it('shows only an empty state when the Environment has no sets', async () => {
    panel.loadVariableSets.mockResolvedValue([]);
    renderCard();
    expect(await screen.findByText('No Variable Sets to bind')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Effective env' })).toBeNull();
  });

  it('lists one row per YAML Service with the bound set names in binding order', async () => {
    panel.loadBindings.mockResolvedValue([{ service: 'web', setIds: [2, 1] }]);
    renderCard();
    expect(await screen.findByText('web')).toBeTruthy();
    expect(screen.getByText('worker')).toBeTruthy();
    const tags = Array.from(document.querySelectorAll('.ant-select-selection-item-content')).map((node) => node.textContent);
    expect(tags).toEqual(['cache', 'db']);
    expect(screen.getAllByRole('button', { name: /Effective env/ })).toHaveLength(2);
  });

  it('binds a set by saving the Service full ordered id list', async () => {
    panel.loadBindings.mockResolvedValue([{ service: 'web', setIds: [2] }]);
    const user = userEvent.setup();
    renderCard();
    await screen.findByText('web');
    await user.click(screen.getByRole('combobox', { name: 'Variable Sets for web' }));
    await user.click(await screen.findByTitle('db'));
    await waitFor(() => expect(panel.saveBindings).toHaveBeenCalledWith(7, 'web', [2, 1]));
  });

  it('builds a second pick on the first when the first save has not returned', async () => {
    let release: () => void = () => {};
    panel.saveBindings.mockImplementationOnce(() => new Promise<void>((resolve) => (release = resolve)));
    const user = userEvent.setup();
    renderCard();
    await screen.findByText('web');
    await user.click(screen.getByRole('combobox', { name: 'Variable Sets for web' }));
    await user.click(await screen.findByTitle('cache'));
    await user.click(await screen.findByTitle('db'));
    release();
    await waitFor(() => expect(panel.saveBindings).toHaveBeenCalledTimes(2));
    expect(panel.saveBindings).toHaveBeenNthCalledWith(1, 7, 'web', [2]);
    expect(panel.saveBindings).toHaveBeenNthCalledWith(2, 7, 'web', [2, 1]);
  });

  it('shows a stale binding with a warning and removes it with an empty list', async () => {
    panel.loadBindings.mockResolvedValue([{ service: 'old-api', setIds: [1] }]);
    const user = userEvent.setup();
    renderCard();
    expect(await screen.findByText('old-api')).toBeTruthy();
    expect(screen.getByText(/not in YAML · bound to db/)).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Remove' }));
    await waitFor(() => expect(panel.saveBindings).toHaveBeenCalledWith(7, 'old-api', []));
  });

  it('opens the Effective env modal with the server sources, masking a secret and marking a YAML override', async () => {
    panel.loadEffectiveVariables.mockResolvedValue([
      { key: 'LOG_LEVEL', overrides: ['environment'], secret: false, source: 'compose', value: 'debug' },
      { key: 'DB_PASSWORD', overrides: [], secret: true, source: { set: { id: 1, name: 'db' } }, value: 'hunter2' },
      { key: 'LOG_LEVEL', overrides: [], secret: false, source: 'yaml', value: '' },
    ]);
    const user = userEvent.setup();
    renderCard();
    await screen.findByText('web');
    await user.click(screen.getAllByRole('button', { name: /Effective env/ })[0]);
    const dialog = await screen.findByRole('dialog');
    expect(await within(dialog).findByText('DB_PASSWORD')).toBeTruthy();
    expect(panel.loadEffectiveVariables).toHaveBeenCalledWith(7, 'web');
    expect(within(dialog).queryByText('hunter2')).toBeNull();
    expect(within(dialog).getByText('overridden by YAML')).toBeTruthy();
    expect(within(dialog).getByText('set in YAML, wins')).toBeTruthy();
    expect(within(dialog).getByText('Environment')).toBeTruthy();
    await user.click(within(dialog).getByRole('button', { name: 'Reveal value' }));
    expect(within(dialog).getByText('hunter2')).toBeTruthy();
  });
});
