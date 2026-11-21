// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Database, RedisResponse } from '../data/types';
import { DatabaseRedisTab } from './DatabaseRedisTab';

const api = vi.hoisted(() => ({
  redisKey: vi.fn(),
  redisKeys: vi.fn(),
  runRedisCommand: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

const cache: Database = {
  cpus: 0,
  createdAt: '2026-11-14T00:00:00Z',
  defaultDatabase: '0',
  deleted: false,
  desired: 'running',
  engine: 'redis',
  host: 'relist-db-2',
  hostReadable: 'cache.db.internal',
  id: 2,
  image: 'redis:8',
  job: '',
  lastError: '',
  memoryMb: 0,
  name: 'Cache',
  port: 6379,
  settingsOutdated: false,
  slug: 'cache',
  status: 'running',
  user: 'default',
  version: '8',
  warnings: [],
};

function ok(reply: RedisResponse['reply']): RedisResponse {
  return { durationMs: 1, reply };
}

function view(db: Database = cache) {
  return render(
    <SWRConfig value={{ dedupingInterval: 0, provider: () => new Map() }}>
      <App>
        <DatabaseRedisTab db={db} />
      </App>
    </SWRConfig>,
  );
}

async function enter(command: string) {
  const box = screen.getByLabelText('Command');
  await userEvent.clear(box);
  await userEvent.type(box, `${command}{Enter}`);
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  api.runRedisCommand.mockResolvedValue(ok({ type: 'bulk', value: 'v' }));
  api.redisKeys.mockResolvedValue({ cursor: '0', keys: [] });
  api.redisKey.mockResolvedValue({ items: [], key: 'k', more: false, total: 0, ttlMs: -1, type: 'string' });
});

describe('DatabaseRedisTab', () => {
  it('posts the line on Enter, read-only, to database 0', async () => {
    view();
    await enter('GET k');
    await waitFor(() => expect(api.runRedisCommand).toHaveBeenCalled());
    expect(api.runRedisCommand.mock.calls[0][1]).toEqual({ command: 'GET k', confirmDangerous: false, db: 0, writes: false });
    expect(await screen.findByText('"v"')).toBeTruthy();
    expect((screen.getByLabelText('Command') as HTMLInputElement).value).toBe('');
  });

  it('asks before a dangerous command and resends it confirmed', async () => {
    api.runRedisCommand.mockResolvedValueOnce({ durationMs: 0, needsConfirm: true, reply: null });
    view();
    await userEvent.click(screen.getByRole('switch', { name: /allow writes/i }));
    expect(screen.getByText('Writes allowed')).toBeTruthy();
    await enter('FLUSHALL');
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText(/FLUSHALL can't be undone/)).toBeTruthy();
    expect(api.runRedisCommand).toHaveBeenCalledTimes(1);

    await userEvent.click(within(dialog).getByRole('button', { name: /run/i }));
    await waitFor(() => expect(api.runRedisCommand).toHaveBeenCalledTimes(2));
    expect(api.runRedisCommand.mock.calls[1][1]).toEqual({ command: 'FLUSHALL', confirmDangerous: true, db: 0, writes: true });
  });

  it('shows a refused command in the log', async () => {
    api.runRedisCommand.mockResolvedValueOnce({ durationMs: 0, refused: 'writes are off', reply: null });
    view();
    await enter('SET k v');
    expect(await screen.findByText('writes are off')).toBeTruthy();
    expect(screen.getByText('SET k v')).toBeTruthy();
  });

  it('recalls the last command with the Up key', async () => {
    view();
    await enter('GET one');
    await enter('GET two');
    await waitFor(() => expect(api.runRedisCommand).toHaveBeenCalledTimes(2));
    const box = screen.getByLabelText('Command') as HTMLInputElement;
    await userEvent.click(box);
    await userEvent.keyboard('{ArrowUp}');
    expect(box.value).toBe('GET two');
    await userEvent.keyboard('{ArrowUp}');
    expect(box.value).toBe('GET one');
    await userEvent.keyboard('{ArrowDown}');
    expect(box.value).toBe('GET two');
  });

  it('scans keys and loads more with the cursor', async () => {
    api.redisKeys
      .mockResolvedValueOnce({ cursor: '17', keys: [{ key: 'user:1', ttlMs: -1, type: 'hash' }] })
      .mockResolvedValueOnce({ cursor: '0', keys: [{ key: 'user:2', ttlMs: 5000, type: 'string' }] });
    view();
    await userEvent.click(screen.getByRole('button', { name: 'Scan' }));
    expect(await screen.findByText('user:1')).toBeTruthy();
    expect(screen.getByText('no expiry')).toBeTruthy();
    expect(api.redisKeys.mock.calls[0]).toEqual([2, 0, '*', '0']);

    await userEvent.click(screen.getByRole('button', { name: 'Load more' }));
    expect(await screen.findByText('user:2')).toBeTruthy();
    expect(api.redisKeys.mock.calls[1]).toEqual([2, 0, '*', '17']);
    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull();
  });

  it('loads a key by clicking it and notes a value cut at 1,000', async () => {
    api.redisKeys.mockResolvedValueOnce({ cursor: '0', keys: [{ key: 'user:1', ttlMs: -1, type: 'hash' }] });
    api.redisKey.mockResolvedValueOnce({
      items: [{ field: 'name', value: 'Ada' }],
      key: 'user:1',
      more: true,
      total: 1500,
      ttlMs: -1,
      type: 'hash',
    });
    view();
    await userEvent.click(screen.getByRole('button', { name: 'Scan' }));
    await userEvent.click(await screen.findByText('user:1'));
    await waitFor(() => expect(api.redisKey).toHaveBeenCalledWith(2, 0, 'user:1'));
    expect(await screen.findByText('Ada')).toBeTruthy();
    expect(screen.getByText(/first 1,000 of 1,500/i)).toBeTruthy();
  });

  it('cannot run on a stopped Database', async () => {
    view({ ...cache, status: 'stopped' });
    expect(await screen.findByText(/not running/i)).toBeTruthy();
    expect((screen.getByLabelText('Command') as HTMLInputElement).disabled).toBe(true);
  });
});
