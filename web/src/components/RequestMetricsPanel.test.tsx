// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor } from '@testing-library/react';
import { App } from 'antd';
import { MemoryRouter } from 'react-router';
import { SWRConfig } from 'swr';
import { describe, expect, it, vi } from 'vitest';

import type { RequestMetrics, RequestWindow } from '../data/types';
import { RequestMetricsPanel } from './RequestMetricsPanel';

function metrics(over: Partial<RequestMetrics> = {}): RequestMetrics {
  return {
    window: '1h',
    partial: false,
    accessLogEnabled: true,
    accessLogFiltered: false,
    retentionDays: 1,
    summary: { requests: 1234, c1xx: 0, c2xx: 1200, c3xx: 0, c4xx: 4, c5xx: 30, cother: 0, p50Ms: 12, p90Ms: 80, p99Ms: 410 },
    buckets: [{ at: 60_000, c1xx: 0, c2xx: 10, c3xx: 0, c4xx: 0, c5xx: 1, cother: 0, p50Ms: 12, p90Ms: 80, p99Ms: 410 }],
    ...over,
  };
}

function renderPanel(load: (w: RequestWindow) => Promise<RequestMetrics>) {
  return render(
    <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
      <MemoryRouter>
        <App>
          <RequestMetricsPanel load={load} swrKey="test" />
        </App>
      </MemoryRouter>
    </SWRConfig>,
  );
}

describe('RequestMetricsPanel', () => {
  it('shows the summary and both charts', async () => {
    renderPanel(vi.fn().mockResolvedValue(metrics()));
    expect(await screen.findByText('1,234')).toBeTruthy();
    expect(screen.getByText('2.43%')).toBeTruthy();
    expect(screen.getAllByText('410 ms').length).toBeGreaterThan(0);
    expect(screen.getByText('Status')).toBeTruthy();
    expect(screen.getByText('Latency')).toBeTruthy();
  });

  it('replaces the charts with an Alert when the Access log is off', async () => {
    renderPanel(vi.fn().mockResolvedValue(metrics({ accessLogEnabled: false })));
    expect(await screen.findByText('The Access log is off.')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Traefik' }).getAttribute('href')).toBe('/traefik');
    expect(screen.queryByText('Status')).toBeNull();
  });

  it('warns above the charts when the Access log is filtered', async () => {
    renderPanel(vi.fn().mockResolvedValue(metrics({ accessLogFiltered: true })));
    expect(await screen.findByText(/The Access log is filtered/)).toBeTruthy();
    expect(screen.getByText('Status')).toBeTruthy();
  });

  it('offers only the windows the retention covers', async () => {
    renderPanel(vi.fn().mockResolvedValue(metrics({ retentionDays: 7 })));
    await waitFor(() => expect(screen.getByText('7d')).toBeTruthy());
    expect(screen.queryByText('30d')).toBeNull();
    expect(screen.getByText('24h')).toBeTruthy();
  });

  it('shows the server error with a Retry', async () => {
    renderPanel(vi.fn().mockRejectedValue(new Error('boom')));
    expect(await screen.findByText('boom')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeTruthy();
  });
});
