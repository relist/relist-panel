// @vitest-environment jsdom
import '../test/dom-setup';

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Environment } from '../data/types';
import { ValueCell, VariablesEditor, VariablesPanel } from './VariablesPanel';

const panel = vi.hoisted(() => ({
  deleteVariable: vi.fn(),
  isPending: vi.fn(() => false),
  replaceVariables: vi.fn(),
  saveVariable: vi.fn(),
}));

vi.mock('../data/store', () => ({ usePanel: () => panel }));

const environment: Environment = {
  composes: [],
  createdAt: '',
  id: 1,
  name: 'production',
  projectId: 1,
  variables: [{ environmentId: 1, id: 1, key: 'SIGNING', secret: true, updatedAt: '2026-10-01T00:00:00Z', value: 'old' }],
};

describe('VariablesPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    panel.replaceVariables.mockResolvedValue(undefined);
  });

  const withSecondVariable: Environment = {
    ...environment,
    variables: [
      ...environment.variables,
      { environmentId: 1, id: 2, key: 'TZ', secret: false, updatedAt: '2026-10-01T00:00:00Z', value: 'UTC' },
    ],
  };

  function renderPanel(env: Environment = environment) {
    return render(
      <App>
        <VariablesPanel environment={env} />
      </App>,
    );
  }

  it('keeps an existing secret secret on a paste and guesses a new key from its name', async () => {
    const user = userEvent.setup();
    renderPanel();

    await user.click(screen.getByRole('button', { name: /Paste .env/ }));
    fireEvent.change(screen.getByPlaceholderText(/DATABASE_URL=/), { target: { value: 'SIGNING=new\nDB_PASSWORD=pw\nTZ=UTC' } });
    await user.click(screen.getByRole('checkbox', { name: /Replace all/ }));
    await user.click(screen.getByRole('button', { name: /Replace 1 variable with 3/ }));

    await waitFor(() =>
      expect(panel.replaceVariables).toHaveBeenCalledWith(
        1,
        [
          { key: 'SIGNING', secret: true, value: 'new' },
          { key: 'DB_PASSWORD', secret: true, value: 'pw' },
          { key: 'TZ', secret: false, value: 'UTC' },
        ],
        { prune: true },
      ),
    );
  });

  it('merges by default: counts only new and changed keys and keeps the ones the paste drops', async () => {
    const user = userEvent.setup();
    renderPanel(withSecondVariable);

    await user.click(screen.getByRole('button', { name: /Paste .env/ }));
    expect((screen.getByRole('checkbox', { name: /Replace all/ }) as HTMLInputElement).checked).toBe(false);
    fireEvent.change(screen.getByPlaceholderText(/DATABASE_URL=/), { target: { value: 'TZ=UTC\nLOG_LEVEL=info' } });
    expect(screen.getByText(/keeps SIGNING/)).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Apply 1 change' }));

    await waitFor(() =>
      expect(panel.replaceVariables).toHaveBeenCalledWith(
        1,
        [
          { key: 'TZ', secret: false, value: 'UTC' },
          { key: 'LOG_LEVEL', secret: false, value: 'info' },
        ],
        { prune: false },
      ),
    );
  });

  it('offers nothing to apply when a merge changes no value', async () => {
    const user = userEvent.setup();
    renderPanel(withSecondVariable);

    await user.click(screen.getByRole('button', { name: /Paste .env/ }));
    fireEvent.change(screen.getByPlaceholderText(/DATABASE_URL=/), { target: { value: 'TZ=UTC' } });
    expect(screen.getByRole('button', { name: 'No changes' }).hasAttribute('disabled')).toBe(true);
  });
});

describe('VariablesEditor, bare', () => {
  const rows = (count: number) =>
    Array.from({ length: count }, (_, index) => ({ id: index + 1, key: `KEY_${index + 1}`, secret: index === 0, value: `v${index + 1}` }));

  function renderBare(count: number, onReplace?: () => Promise<void>) {
    return render(
      <App>
        <VariablesEditor
          addLabel="Add entry"
          bare
          deleteNote=""
          description="a description"
          emptyDescription="Nothing here yet."
          emptyTitle="Empty"
          isDeleting={() => false}
          loading={false}
          noun="entries"
          notice="a notice"
          onDelete={vi.fn()}
          onReplace={onReplace}
          onSave={vi.fn()}
          saving={false}
          title="A title"
          variables={rows(count)}
        />
      </App>,
    );
  }

  it('renders the table alone: no title, description, notice or secret/plain tags', () => {
    renderBare(2);
    expect(screen.queryByText('A title')).toBeNull();
    expect(screen.queryByText('a description')).toBeNull();
    expect(screen.queryByText('a notice')).toBeNull();
    expect(screen.queryByText('plain')).toBeNull();
    expect(screen.getByText('KEY_1')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Edit KEY_1' })).toBeTruthy();
  });

  it('puts add and paste as links under the table', () => {
    renderBare(1, vi.fn());
    expect(screen.getByRole('button', { name: /Add entry/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: /Paste .env/ })).toBeTruthy();
  });

  it('shows one line instead of a table when empty, and no paste link without onReplace', () => {
    renderBare(0);
    expect(screen.getByText('Nothing here yet.')).toBeTruthy();
    expect(screen.queryByRole('table')).toBeNull();
    expect(screen.queryByRole('button', { name: /Paste/ })).toBeNull();
  });

  it('offers the key filter only above ten rows', () => {
    const { unmount } = renderBare(10);
    expect(screen.queryByPlaceholderText('Filter by key')).toBeNull();
    unmount();
    renderBare(11);
    expect(screen.getByPlaceholderText('Filter by key')).toBeTruthy();
  });
});

describe('ValueCell, withheld secret', () => {
  it('shows a secret the API withheld as dots, with nothing to reveal or copy', () => {
    render(<App><ValueCell variable={{ secret: true, value: '' }} /></App>);
    expect(screen.getByText('••••')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Reveal value' })).toBeNull();
    expect(screen.queryByRole('button', { name: /Copy/ })).toBeNull();
    expect(screen.getByLabelText('Hidden: needs the Editor role')).toBeTruthy();
  });

  it('still reveals a secret whose value came through', async () => {
    render(<App><ValueCell variable={{ secret: true, value: 's3cret' }} /></App>);
    expect(screen.getByRole('button', { name: 'Reveal value' })).toBeTruthy();
  });
});
