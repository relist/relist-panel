// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Compose, VariableSet } from '../data/types';
import { VariableSetsCard } from './VariableSetsCard';

const panel = vi.hoisted(() => ({
  createVariableSet: vi.fn(),
  deleteSetEntry: vi.fn(),
  deleteVariableSet: vi.fn(),
  isPending: vi.fn(() => false),
  renameVariableSet: vi.fn(),
  saveSetEntry: vi.fn(),
}));

vi.mock('../data/store', () => ({ usePanel: () => panel }));

const compose = { id: 7, name: 'shop' } as Compose;
const dbSet: VariableSet = {
  createdAt: '',
  entries: [
    { id: 10, key: 'DB_PASSWORD', secret: true, setId: 1, value: 'hunter2' },
    { id: 11, key: 'DB_HOST', secret: false, setId: 1, value: 'db' },
  ],
  boundServices: ['api', 'worker'],
  composeId: 7,
  id: 1,
  name: 'db',
  updatedAt: '',
};

function renderCard(sets: VariableSet[] | null = [dbSet], error?: string) {
  const onError = vi.fn();
  const onReload = vi.fn().mockResolvedValue(undefined);
  render(
    <App>
      <VariableSetsCard compose={compose} error={error} onError={onError} onReload={onReload} sets={sets ?? undefined} />
    </App>,
  );
  return { onError, onReload };
}

describe('VariableSetsCard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    panel.isPending.mockReturnValue(false);
  });

  it('shows the empty state with a single New set button', async () => {
    renderCard([]);
    expect(await screen.findByText('No Variable Sets yet')).toBeTruthy();
    expect(screen.getAllByRole('button', { name: /New set/ })).toHaveLength(1);
    expect(screen.getByText(/not usable as/)).toBeTruthy();
  });

  it('lists a set with its entry count and bound-Service tags, and shows masked entries when expanded', async () => {
    const user = userEvent.setup();
    renderCard();
    expect(await screen.findByText('2 entries')).toBeTruthy();
    expect(screen.getByText('api')).toBeTruthy();
    expect(screen.getByText('worker')).toBeTruthy();
    await user.click(screen.getByText('db'));
    expect(await screen.findByText('DB_PASSWORD')).toBeTruthy();
    expect(screen.queryByText('hunter2')).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Reveal value' }));
    expect(screen.getByText('hunter2')).toBeTruthy();
  });

  it('creates a set from the name form and reloads', async () => {
    panel.createVariableSet.mockResolvedValue(undefined);
    const user = userEvent.setup();
    const { onReload } = renderCard();
    await screen.findByText('2 entries');
    await user.click(screen.getByRole('button', { name: /New set/ }));
    const dialog = await screen.findByRole('dialog');
    await user.type(within(dialog).getByLabelText('Name'), 'Bad Name');
    await user.click(within(dialog).getByRole('button', { name: 'Create set' }));
    expect(await within(dialog).findByText(/Lowercase letters, digits and dashes, starting/)).toBeTruthy();
    expect(panel.createVariableSet).not.toHaveBeenCalled();
    await user.clear(within(dialog).getByLabelText('Name'));
    await user.type(within(dialog).getByLabelText('Name'), 'cache');
    await user.click(within(dialog).getByRole('button', { name: 'Create set' }));
    await waitFor(() => expect(panel.createVariableSet).toHaveBeenCalledWith(7, 'cache'));
    await waitFor(() => expect(onReload).toHaveBeenCalled());
  });

  it('renames in place', async () => {
    panel.renameVariableSet.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderCard();
    await screen.findByText('2 entries');
    await user.click(screen.getByRole('button', { name: 'Rename db' }));
    const input = screen.getByLabelText('New name for db');
    await user.clear(input);
    await user.type(input, 'database');
    await user.click(screen.getByRole('button', { name: 'Save name' }));
    await waitFor(() => expect(panel.renameVariableSet).toHaveBeenCalledWith(7, 1, 'database'));
  });

  it('names the bound Services in the delete confirmation and deletes without a refusal path', async () => {
    panel.deleteVariableSet.mockResolvedValue(undefined);
    const user = userEvent.setup();
    const { onError, onReload } = renderCard();
    await screen.findByText('2 entries');
    await user.click(screen.getByRole('button', { name: 'Delete db' }));
    expect(await screen.findByText('Unbinds it from api, worker. Takes effect on the next deploy.')).toBeTruthy();
    await user.click(await screen.findByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(panel.deleteVariableSet).toHaveBeenCalledWith(7, 1));
    await waitFor(() => expect(onReload).toHaveBeenCalled());
    expect(onError).not.toHaveBeenCalled();
  });

  it('deletes an unbound set with a plain confirmation', async () => {
    const user = userEvent.setup();
    renderCard([{ ...dbSet, boundServices: [] }]);
    await screen.findByText('2 entries');
    expect(screen.getByText('· not bound')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Delete db' }));
    expect(await screen.findByText('Its entries are deleted with it.')).toBeTruthy();
  });

  it('saves an entry through the store and reloads the sets', async () => {
    panel.saveSetEntry.mockResolvedValue(undefined);
    const user = userEvent.setup();
    const { onReload } = renderCard();
    await user.click(await screen.findByText('db'));
    await user.click(await screen.findByRole('button', { name: 'Edit DB_HOST' }));
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByRole('button', { name: 'Save changes' }));
    await waitFor(() => expect(panel.saveSetEntry).toHaveBeenCalledWith(7, 1, expect.objectContaining({ key: 'DB_HOST' }), 11));
    await waitFor(() => expect(onReload).toHaveBeenCalled());
  });

  it('shows a load error with Retry when there is no list', async () => {
    const user = userEvent.setup();
    const { onReload } = renderCard(null, 'boom');
    expect(await screen.findByText('boom')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Retry' }));
    expect(onReload).toHaveBeenCalled();
  });
});
