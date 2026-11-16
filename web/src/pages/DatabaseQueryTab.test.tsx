// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Database, QueryResponse } from '../data/types';

const api = vi.hoisted(() => ({
  databaseLogicalDatabases: vi.fn(),
  databaseQuerySchema: vi.fn(),
  runDatabaseQuery: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));
vi.mock('../monaco', () => ({}));
vi.mock('@monaco-editor/react', async () => {
  const React = await import('react');
  return {
    default: (p: { onChange?: (v: string) => void; onMount?: (e: unknown, m: unknown) => void; value: string; language: string }) => {
      React.useEffect(() => {
        p.onMount?.(
          { addCommand: () => undefined, getModel: () => null, getSelection: () => null },
          { KeyCode: { Enter: 3 }, KeyMod: { CtrlCmd: 2048 }, editor: { setModelMarkers: () => undefined } },
        );
        // eslint-disable-next-line react-hooks/exhaustive-deps
      }, []);
      return <textarea aria-label="SQL" data-language={p.language} onChange={(e) => p.onChange?.(e.target.value)} value={p.value} />;
    },
  };
});

const { DatabaseQueryTab } = await import('./DatabaseQueryTab');

const shop: Database = {
  cpus: 0,
  createdAt: '2026-11-14T00:00:00Z',
  defaultDatabase: 'postgres',
  deleted: false,
  desired: 'running',
  engine: 'postgres',
  host: 'relist-db-1',
  hostReadable: 'shop.db.internal',
  id: 1,
  image: 'postgres:18',
  job: '',
  lastError: '',
  memoryMb: 0,
  name: 'Shop',
  port: 5432,
  settingsOutdated: false,
  slug: 'shop',
  status: 'running',
  user: 'postgres',
  version: '18',
  warnings: [],
};

function answer(over: Partial<QueryResponse> = {}): QueryResponse {
  return {
    cancelled: false,
    durationMs: 3,
    error: null,
    results: [
      {
        affected: null,
        columns: [{ name: 'n', type: 'int4' }],
        command: 'SELECT 1',
        durationMs: 1,
        rows: [['1']],
        statementIndex: 0,
        truncated: false,
      },
    ],
    stopped: '',
    ...over,
  };
}

function view(db: Database = shop) {
  return render(
    <SWRConfig value={{ dedupingInterval: 0, provider: () => new Map() }}>
      <App>
        <DatabaseQueryTab db={db} />
      </App>
    </SWRConfig>,
  );
}

async function typeSql(sql: string) {
  const box = await screen.findByLabelText('SQL');
  await userEvent.clear(box);
  await userEvent.click(box);
  await userEvent.paste(sql);
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  api.databaseLogicalDatabases.mockResolvedValue({ databases: ['shop'] });
  api.databaseQuerySchema.mockResolvedValue([{ name: 'public', tables: ['items'] }]);
  api.runDatabaseQuery.mockResolvedValue(answer());
});

describe('DatabaseQueryTab', () => {
  it('runs the editor text read-only on the default database', async () => {
    view();
    await typeSql('SELECT 1');
    await userEvent.click(screen.getByRole('button', { name: /^run/i }));
    await waitFor(() => expect(api.runDatabaseQuery).toHaveBeenCalled());
    expect(api.runDatabaseQuery.mock.calls[0][1]).toEqual({ database: 'postgres', preview: false, sql: 'SELECT 1', writes: false });
    expect(await screen.findByText('1 · SELECT 1')).toBeTruthy();
  });

  it('sends writes after the switch is on, and says so', async () => {
    view();
    await userEvent.click(screen.getByRole('switch', { name: /allow writes/i }));
    expect(screen.getByText('Writes allowed')).toBeTruthy();
    await typeSql('DELETE FROM t');
    await userEvent.click(screen.getByRole('button', { name: /^run/i }));
    await waitFor(() => expect(api.runDatabaseQuery.mock.calls[0][1].writes).toBe(true));
  });

  it('shows what stopped the batch and the error message', async () => {
    api.runDatabaseQuery.mockResolvedValueOnce(answer({ stopped: 'Stopped after 1,000 rows.' }));
    view();
    await typeSql('SELECT 1');
    await userEvent.click(screen.getByRole('button', { name: /^run/i }));
    expect(await screen.findByText('Stopped after 1,000 rows.')).toBeTruthy();

    api.runDatabaseQuery.mockResolvedValueOnce(
      answer({ error: { message: 'syntax error at or near "SELEC"', position: 0, statementIndex: 0, statementStart: 0 }, results: [] }),
    );
    await userEvent.click(screen.getByRole('button', { name: /^run/i }));
    expect(await screen.findByText('syntax error at or near "SELEC"')).toBeTruthy();
  });

  it('cancels by aborting the request', async () => {
    let signal: AbortSignal | undefined;
    api.runDatabaseQuery.mockImplementationOnce(
      (_id: number, _body: unknown, s?: AbortSignal) =>
        new Promise((_resolve, reject) => {
          signal = s;
          s?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
        }),
    );
    view();
    await typeSql('SELECT pg_sleep(60)');
    await userEvent.click(screen.getByRole('button', { name: /^run/i }));
    await userEvent.click(await screen.findByRole('button', { name: /cancel/i }));
    expect(signal?.aborted).toBe(true);
    expect(await screen.findByText('Cancelled.')).toBeTruthy();
    expect(screen.getByRole('button', { name: /^run/i })).toBeTruthy();
  });

  it('runs SELECT * LIMIT 100 for a table clicked in the tree', async () => {
    view();
    await userEvent.click(await screen.findByText('items'));
    await waitFor(() => expect(api.runDatabaseQuery).toHaveBeenCalled());
    expect(api.runDatabaseQuery.mock.calls[0][1].sql).toBe('SELECT * FROM "public"."items" LIMIT 100');
  });

  it('quotes a MySQL table with backticks and uses the first listed database', async () => {
    api.databaseLogicalDatabases.mockResolvedValue({ databases: ['app', 'zoo'] });
    api.databaseQuerySchema.mockResolvedValue([{ name: 'app', tables: ['users'] }]);
    view({ ...shop, defaultDatabase: '', engine: 'mysql', image: 'mysql:8.4' });
    await userEvent.click(await screen.findByText('users'));
    await waitFor(() => expect(api.runDatabaseQuery).toHaveBeenCalled());
    expect(api.runDatabaseQuery.mock.calls[0][1]).toEqual({ database: 'app', preview: false, sql: 'SELECT * FROM `users` LIMIT 100', writes: false });
    expect(screen.getByLabelText('SQL').getAttribute('data-language')).toBe('mysql');
  });

  it('keeps the last runs and lists them in History', async () => {
    view();
    await typeSql('SELECT 42');
    await userEvent.click(screen.getByRole('button', { name: /^run/i }));
    await screen.findByText('1 · SELECT 1');
    await userEvent.click(screen.getByRole('button', { name: /history/i }));
    const drawer = await screen.findByRole('dialog');
    expect(within(drawer).getByText('SELECT 42')).toBeTruthy();
    expect(JSON.parse(localStorage.getItem('relist.query.history.1') ?? '[]')).toHaveLength(1);
  });

  it('cannot run on a stopped Database', async () => {
    view({ ...shop, status: 'stopped' });
    expect(await screen.findByText(/not running/i)).toBeTruthy();
    expect((screen.getByRole('button', { name: /^run/i }) as HTMLButtonElement).disabled).toBe(true);
  });
});

describe('Preview', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.databaseLogicalDatabases.mockResolvedValue({ databases: ['shop'] });
    api.databaseQuerySchema.mockResolvedValue([]);
  });

  function renderTab() {
    render(
      <SWRConfig value={{ provider: () => new Map() }}>
        <App>
          <DatabaseQueryTab db={shop} />
        </App>
      </SWRConfig>,
    );
  }

  it('shows Preview beside Run only while writes are on', async () => {
    renderTab();
    expect(await screen.findByRole('button', { name: 'Run' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Preview' })).toBeNull();
    await userEvent.setup().click(screen.getByRole('switch', { name: 'Allow writes' }));
    expect(await screen.findByRole('button', { name: 'Preview' })).toBeTruthy();
  });

  it('renders what a Preview would change, and what it cannot undo', async () => {
    api.runDatabaseQuery.mockResolvedValue({
      cancelled: false,
      durationMs: 5,
      error: null,
      preview: true,
      results: [{ affected: 3412, columns: [], command: 'DELETE 3412', durationMs: 4, rows: [], statementIndex: 0, target: 'orders', truncated: false, verb: 'delete' }],
      stopped: '',
    });
    renderTab();
    const user = userEvent.setup();
    await user.click(await screen.findByRole('switch', { name: 'Allow writes' }));
    await user.type(screen.getByLabelText('SQL'), 'DELETE FROM orders');
    await user.click(screen.getByRole('button', { name: 'Preview' }));
    expect(await screen.findByText('Would delete 3,412 rows from orders')).toBeTruthy();
    expect(screen.getByText(/It cannot undo sequence values/)).toBeTruthy();
    expect(api.runDatabaseQuery).toHaveBeenCalledWith(
      shop.id,
      expect.objectContaining({ preview: true, writes: true }),
      expect.anything(),
    );
  });

  it('renders a refused Preview', async () => {
    api.runDatabaseQuery.mockResolvedValue({
      cancelled: false,
      durationMs: 1,
      error: { message: 'Preview runs only INSERT, UPDATE and DELETE statements. Statement 1 starts with TRUNCATE.', position: null, statementIndex: 0, statementStart: 0 },
      preview: true,
      results: [],
      stopped: '',
    });
    renderTab();
    const user = userEvent.setup();
    await user.click(await screen.findByRole('switch', { name: 'Allow writes' }));
    await user.type(screen.getByLabelText('SQL'), 'TRUNCATE t');
    await user.click(screen.getByRole('button', { name: 'Preview' }));
    expect(await screen.findByText(/starts with TRUNCATE/)).toBeTruthy();
  });
});
