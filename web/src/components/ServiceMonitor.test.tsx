// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen } from '@testing-library/react';
import { App } from 'antd';
import { MemoryRouter } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

import type { Compose } from '../data/types';

// The collector has seen no container yet: right after a deploy, or after the
// panel restarts, the Services list is empty for a moment.
vi.mock('../data/store', () => ({
  usePanel: () => ({
    isPending: () => false,
    loadServiceMetrics: vi.fn(),
    loadServices: () => Promise.resolve([]),
    services: {},
  }),
}));
vi.mock('./ComposeRequests', () => ({ ComposeRequests: () => <div>request metrics</div> }));

import { ServiceMonitor } from './ServiceMonitor';

describe('ServiceMonitor', () => {
  it('keeps the request metrics under the empty state while no service is known', async () => {
    render(
      <MemoryRouter>
        <App>
          <ServiceMonitor compose={{ id: 3 } as Compose} onOpenEditor={() => {}} />
        </App>
      </MemoryRouter>,
    );
    expect(await screen.findByText('Nothing to monitor yet')).toBeTruthy();
    expect(screen.getByText('request metrics')).toBeTruthy();
  });
});
