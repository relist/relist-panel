// @vitest-environment jsdom
import '../test/dom-setup';

import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
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
  replaceSetEntries: vi.fn(),
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

const cacheSet: VariableSet = {
  ...dbSet,
  boundServices: [],
  entries: [{ id: 20, key: 'REDIS_URL', secret: false, setId: 2, value: 'redis://cache' }],
  id: 2,
  name: 'cache',
};

function renderCard(sets: VariableSet[] | null = [dbSet], error?: string) {
  const onError = vi.fn();
  const onReload = vi.fn().mockResolvedValue(undefined);
  const card = (next: VariableSet[] | null) => (
    <App>
      <VariableSetsCard compose={compose} error={error} onError={onError} onReload={onReload} sets={next ?? undefined} />
    </App>
  );
  const view = render(card(sets));
  return { onError, onReload, rerenderSets: (next: VariableSet[]) => view.rerender(card(next)) };
}

describe('VariableSetsCard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    panel.isPending.mockReturnValue(false);
  });

  it('shows the empty state with a single New set button, and explains sets in the title tooltip', async () => {
    const user = userEvent.setup();
    renderCard([]);
    expect(await screen.findByText('No Variable Sets yet')).toBeTruthy();
    expect(screen.getAllByRole('button', { name: /New set/ })).toHaveLength(1);
    await user.hover(screen.getByLabelText('About this card'));
    expect(await screen.findByText(/Not usable as/)).toBeTruthy();
  });

  it('shows one tab per set with its entry count, the first one open with masked entries and its Services', async () => {
    const user = userEvent.setup();
    renderCard([dbSet, cacheSet]);
    expect(await screen.findByRole('tab', { name: /db\s*2/ })).toBeTruthy();
    expect(screen.getByRole('tab', { name: /cache\s*1/ })).toBeTruthy();
    expect(screen.getByText('DB_PASSWORD')).toBeTruthy();
    expect(screen.getByText('api')).toBeTruthy();
    expect(screen.getByText('worker')).toBeTruthy();
    expect(screen.queryByText('hunter2')).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Reveal value' }));
    expect(screen.getByText('hunter2')).toBeTruthy();
  });

  it('switches to another set and says when it is not bound', async () => {
    const user = userEvent.setup();
    renderCard([dbSet, cacheSet]);
    await user.click(await screen.findByRole('tab', { name: /cache/ }));
    expect(await screen.findByText('REDIS_URL')).toBeTruthy();
    expect(screen.getByText('Not bound to any Service yet')).toBeTruthy();
  });

  it('creates a set from the name form, reloads, and opens the new tab', async () => {
    panel.createVariableSet.mockResolvedValue(undefined);
    const user = userEvent.setup();
    const { onReload, rerenderSets } = renderCard();
    await screen.findByText('DB_PASSWORD');
    expect(screen.getAllByRole('button', { name: /New set/ })).toHaveLength(1);
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
    rerenderSets([dbSet, cacheSet]);
    expect(await screen.findByText('REDIS_URL')).toBeTruthy();
  });

  it('renames the open set through a modal', async () => {
    panel.renameVariableSet.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderCard();
    await screen.findByText('DB_PASSWORD');
    await user.click(screen.getByRole('button', { name: 'Rename db' }));
    const dialog = await screen.findByRole('dialog');
    const input = within(dialog).getByLabelText('Name');
    await user.clear(input);
    await user.type(input, 'database');
    await user.click(within(dialog).getByRole('button', { name: 'Rename' }));
    await waitFor(() => expect(panel.renameVariableSet).toHaveBeenCalledWith(7, 1, 'database'));
  });

  it('names the bound Services in the delete confirmation and deletes without a refusal path', async () => {
    panel.deleteVariableSet.mockResolvedValue(undefined);
    const user = userEvent.setup();
    const { onError, onReload } = renderCard();
    await screen.findByText('DB_PASSWORD');
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
    await screen.findByText('DB_PASSWORD');
    await user.click(screen.getByRole('button', { name: 'Delete db' }));
    expect(await screen.findByText('Its entries are deleted with it.')).toBeTruthy();
  });

  it('saves an entry through the store and reloads the sets', async () => {
    panel.saveSetEntry.mockResolvedValue(undefined);
    const user = userEvent.setup();
    const { onReload } = renderCard();
    await user.click(await screen.findByRole('button', { name: 'Edit DB_HOST' }));
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByRole('button', { name: 'Save changes' }));
    await waitFor(() => expect(panel.saveSetEntry).toHaveBeenCalledWith(7, 1, expect.objectContaining({ key: 'DB_HOST' }), 11));
    await waitFor(() => expect(onReload).toHaveBeenCalled());
  });

  it("replaces the open set's entries from a pasted .env and reloads", async () => {
    panel.replaceSetEntries.mockResolvedValue(undefined);
    const user = userEvent.setup();
    const { onReload } = renderCard();
    await user.click(await screen.findByRole('button', { name: /Paste .env/ }));
    fireEvent.change(screen.getByPlaceholderText(/DATABASE_URL=/), { target: { value: 'DB_PASSWORD=new\nDB_PORT=5432' } });
    await user.click(screen.getByRole('checkbox', { name: /Replace all/ }));
    await user.click(screen.getByRole('button', { name: /Replace 2 variables with 2/ }));
    await waitFor(() =>
      expect(panel.replaceSetEntries).toHaveBeenCalledWith(
        7,
        1,
        [
          { key: 'DB_PASSWORD', secret: true, value: 'new' },
          { key: 'DB_PORT', secret: false, value: '5432' },
        ],
        { prune: true },
      ),
    );
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
