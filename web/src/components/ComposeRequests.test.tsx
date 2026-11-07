// @vitest-environment jsdom
import '../test/dom-setup';

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { App } from 'antd';
import { MemoryRouter } from 'react-router';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Compose, RequestMetrics } from '../data/types';

const composeRequestMetrics = vi.fn();
vi.mock('../data/api', () => ({ api: { composeRequestMetrics: (...args: unknown[]) => composeRequestMetrics(...args) } }));
vi.mock('../data/store', () => ({ usePanel: () => ({ projects: [{ id: 1, environments: [{ id: 2, composes: [{ id: 3 }] }] }] }) }));
vi.mock('../data/types', async (original) => ({
  ...(await original<typeof import('../data/types')>()),
  findComposeLocation: () => ({ project: { id: 1 }, environment: { id: 2 } }),
}));

import { ComposeRequests } from './ComposeRequests';

const answer = (hosts: string[]): RequestMetrics => ({
  window: '1h',
  partial: false,
  accessLogEnabled: true,
  accessLogFiltered: false,
  retentionDays: 1,
  hosts,
  summary: { requests: 5, c1xx: 0, c2xx: 5, c3xx: 0, c4xx: 0, c5xx: 0, cother: 0, p50Ms: 1, p90Ms: 2, p99Ms: 3 },
  buckets: [],
});

function renderIt() {
  return render(
    <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
      <MemoryRouter>
        <App>
          <ComposeRequests compose={{ id: 3 } as Compose} />
        </App>
      </MemoryRouter>
    </SWRConfig>,
  );
}

describe('ComposeRequests', () => {
  beforeEach(() => composeRequestMetrics.mockReset());

  it('stays hidden when the Compose has no Domain', async () => {
    composeRequestMetrics.mockResolvedValue(answer([]));
    const { container } = renderIt();
    await waitFor(() => expect(composeRequestMetrics).toHaveBeenCalled());
    await waitFor(() => expect(container.querySelector('[hidden]')).not.toBeNull());
  });

  it('lists All hosts then each host, and asks for the chosen one', async () => {
    composeRequestMetrics.mockResolvedValue(answer(['a.test', 'b.test']));
    renderIt();
    await screen.findByText('Requests');
    expect(composeRequestMetrics).toHaveBeenCalledWith({ projectId: 1, environmentId: 2, composeId: 3 }, '1h', undefined);
    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'Host' }));
    const options = await screen.findAllByRole('option');
    expect(options.map((o) => o.getAttribute('aria-label') ?? o.textContent)).toEqual(['All hosts', 'a.test', 'b.test']);
    fireEvent.click(screen.getByText('a.test', { selector: '.ant-select-item-option-content' }));
    await waitFor(() =>
      expect(composeRequestMetrics).toHaveBeenLastCalledWith({ projectId: 1, environmentId: 2, composeId: 3 }, '1h', 'a.test'),
    );
  });
});
