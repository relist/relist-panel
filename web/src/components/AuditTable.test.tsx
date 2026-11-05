// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { describe, expect, it, vi } from 'vitest';

import type { AuditEvent, AuditPage } from '../data/api';
import { AuditTable } from './AuditTable';

function event(id: number, over: Partial<AuditEvent> = {}): AuditEvent {
  return {
    id, at: 1_800_000_000_000, actorUserId: 1, actor: 'admin@example.com', action: 'compose.update', outcome: 'ok',
    projectId: 1, environmentId: 11, composeId: 101, target: 'shop / production / api', detail: null, ip: '192.0.2.1', ...over,
  };
}

describe('AuditTable', () => {
  it('shows a page and appends the next one on Load more', async () => {
    const load = vi.fn(async (query: { cursor?: number }): Promise<AuditPage> =>
      query.cursor ? { items: [event(1, { action: 'project.update' })], nextCursor: null } : { items: [event(3), event(2, { detail: { key: 'TOKEN' } })], nextCursor: 2 });
    const user = userEvent.setup();
    render(<App><AuditTable load={load} /></App>);
    expect(await screen.findAllByText('compose.update')).toHaveLength(2);
    expect(screen.getByText('key: TOKEN')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: /Load more/ }));
    expect(await screen.findByText('project.update')).toBeTruthy();
    expect(load).toHaveBeenLastCalledWith({ cursor: 2 });
    expect(screen.getAllByText('shop / production / api')).toHaveLength(3);
    expect(screen.queryByRole('button', { name: /Load more/ })).toBeNull();
  });

  it('filters by action from the first page', async () => {
    const load = vi.fn(async (): Promise<AuditPage> => ({ items: [event(1)], nextCursor: null }));
    const user = userEvent.setup();
    render(<App><AuditTable load={load} /></App>);
    await screen.findByText('compose.update');
    await user.type(screen.getByRole('textbox', { name: 'Action' }), 'grant.');
    await user.type(screen.getByRole('textbox', { name: 'Actor' }), 'bob@example.com');
    await user.click(screen.getByRole('button', { name: /Filter/ }));
    await waitFor(() => expect(load).toHaveBeenLastCalledWith({ action: 'grant.', actor: 'bob@example.com' }));
  });

  it('marks a denied event', async () => {
    const load = vi.fn(async (): Promise<AuditPage> => ({ items: [event(1, { outcome: 'denied' })], nextCursor: null }));
    render(<App><AuditTable load={load} /></App>);
    const row = (await screen.findByText('compose.update')).closest('tr')!;
    expect(within(row).getByText('denied')).toBeTruthy();
  });

  it('shows a load error with Retry', async () => {
    const load = vi.fn().mockRejectedValueOnce(new Error('boom')).mockResolvedValue({ items: [event(1)], nextCursor: null });
    const user = userEvent.setup();
    render(<App><AuditTable load={load} /></App>);
    expect(await screen.findByText('boom')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: /Retry/ }));
    expect(await screen.findByText('compose.update')).toBeTruthy();
  });
});
