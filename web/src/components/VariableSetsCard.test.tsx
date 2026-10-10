// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../data/api';
import type { Environment, VariableSet } from '../data/types';
import { VariableSetsCard } from './VariableSetsCard';

const panel = vi.hoisted(() => ({
  createVariableSet: vi.fn(),
  deleteSetEntry: vi.fn(),
  deleteVariableSet: vi.fn(),
  isPending: vi.fn(() => false),
  loadVariableSets: vi.fn(),
  renameVariableSet: vi.fn(),
  saveSetEntry: vi.fn(),
}));

vi.mock('../data/store', () => ({ usePanel: () => panel }));

const environment = { id: 1, name: 'production' } as Environment;
const dbSet: VariableSet = {
  createdAt: '',
  entries: [
    { id: 10, key: 'DB_PASSWORD', secret: true, setId: 1, value: 'hunter2' },
    { id: 11, key: 'DB_HOST', secret: false, setId: 1, value: 'db' },
  ],
  environmentId: 1,
  id: 1,
  name: 'db',
  updatedAt: '',
  usedBy: [{ composeId: 7, composeName: 'shop', service: 'api' }],
};

function renderCard() {
  const onError = vi.fn();
  render(
    <App>
      <VariableSetsCard environment={environment} onError={onError} />
    </App>,
  );
  return { onError };
}

describe('VariableSetsCard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    panel.isPending.mockReturnValue(false);
    panel.loadVariableSets.mockResolvedValue([dbSet]);
  });

  it('shows the empty state with a single New set button', async () => {
    panel.loadVariableSets.mockResolvedValue([]);
    renderCard();
    expect(await screen.findByText('No Variable Sets yet')).toBeTruthy();
    expect(screen.getAllByRole('button', { name: /New set/ })).toHaveLength(1);
    expect(screen.getByText(/not usable as/)).toBeTruthy();
  });

  it('lists a set with its entry count and used-by tags, and shows masked entries when expanded', async () => {
    const user = userEvent.setup();
    renderCard();
    expect(await screen.findByText('2 entries')).toBeTruthy();
    expect(screen.getByText('shop/api')).toBeTruthy();
    await user.click(screen.getByText('db'));
    expect(await screen.findByText('DB_PASSWORD')).toBeTruthy();
    expect(screen.queryByText('hunter2')).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Reveal value' }));
    expect(screen.getByText('hunter2')).toBeTruthy();
  });

  it('creates a set from the name form and reloads', async () => {
    panel.createVariableSet.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderCard();
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
    await waitFor(() => expect(panel.createVariableSet).toHaveBeenCalledWith(1, 'cache'));
    await waitFor(() => expect(panel.loadVariableSets).toHaveBeenCalledTimes(2));
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
    await waitFor(() => expect(panel.renameVariableSet).toHaveBeenCalledWith(1, 1, 'database'));
  });

  it('names who still binds a set when the delete is refused with 409', async () => {
    panel.deleteVariableSet.mockRejectedValue(
      new ApiError(409, 'variable set is bound', undefined, [{ composeId: 7, composeName: 'shop', service: 'api' }]),
    );
    const user = userEvent.setup();
    const { onError } = renderCard();
    await screen.findByText('2 entries');
    await user.click(screen.getByRole('button', { name: 'Delete db' }));
    await user.click(await screen.findByRole('button', { name: 'Delete' }));
    expect(await screen.findByText('Cannot delete db')).toBeTruthy();
    expect(screen.getAllByText('shop/api').length).toBeGreaterThan(1);
    expect(onError).not.toHaveBeenCalled();
  });
});
