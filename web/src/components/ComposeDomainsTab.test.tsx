// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Compose, ComposeDomains, Domain } from '../data/types';
import { ComposeDomainsTab } from './ComposeDomainsTab';

const panel = vi.hoisted(() => ({
  createDomain: vi.fn(),
  deleteDomain: vi.fn(),
  loadDomains: vi.fn(),
  setZeroDowntime: vi.fn(),
  updateDomain: vi.fn(),
}));

vi.mock('../data/store', () => ({ usePanel: () => panel }));

const domain: Domain = {
  certProvider: 'letsencrypt',
  certResolver: '',
  createdAt: '2026-10-09T10:00:00Z',
  entrypoint: '',
  host: 'api.example.com',
  https: true,
  id: 3,
  internalPath: '/',
  middlewares: [],
  path: '/v1',
  port: 3000,
  service: 'web',
  stripPath: false,
  updatedAt: '2026-10-09T10:00:00Z',
};

const compose: Compose = {
  createdAt: '2026-10-09T09:00:00Z',
  domainsChangedAt: null,
  envSlug: '',
  environmentId: 1,
  id: 7,
  lastDeployedAt: null,
  name: 'web',
  projectSlug: '',
  status: { running: 0, total: 0 },
  updatedAt: '2026-10-09T09:00:00Z',
  yamlContent: '',
};

const withDomain: ComposeDomains = { domains: [domain], services: ['db', 'web'] };

/** A promise the test settles by hand, to hold a refetch in flight. */
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, reject, resolve };
}

function renderTab(overrides: Partial<Compose> = {}) {
  return render(<ComposeDomainsTab compose={{ ...compose, ...overrides }} deploying={false} onDeploy={() => undefined} onOpenEditor={() => undefined} onReloadZeroDowntime={async () => undefined} />);
}

beforeEach(() => {
  vi.resetAllMocks();
});

describe('ComposeDomainsTab', () => {
  it('shows a skeleton until the first load settles', async () => {
    const first = deferred<ComposeDomains>();
    panel.loadDomains.mockReturnValue(first.promise);
    const { container } = renderTab();

    expect(container.querySelector('.ant-skeleton')).not.toBeNull();
    first.resolve(withDomain);
    expect(await screen.findByText('api.example.com/v1')).toBeTruthy();
    expect(container.querySelector('.ant-skeleton')).toBeNull();
  });

  it('shows the empty state with an Add domain button', async () => {
    panel.loadDomains.mockResolvedValue({ domains: [], services: ['web'] });
    renderTab();

    expect(await screen.findByText('No domains. Add one to route a Service through Traefik.')).toBeTruthy();
    expect(screen.getByRole('button', { name: /Add domain/ })).toBeTruthy();
  });

  it('renders a row with its link, service:port and HTTPS tag', async () => {
    panel.loadDomains.mockResolvedValue(withDomain);
    renderTab();

    const link = await screen.findByRole('link', { name: 'api.example.com/v1' });
    expect(link.getAttribute('href')).toBe('https://api.example.com/v1');
    expect(link.getAttribute('target')).toBe('_blank');
    expect(screen.getByText('web:3000')).toBeTruthy();
    expect(screen.getByText("Let's Encrypt")).toBeTruthy();
  });

  it('shows the redeploy banner only when domains changed since the last deploy', async () => {
    panel.loadDomains.mockResolvedValue(withDomain);
    const pending = renderTab({ domainsChangedAt: '2026-10-09T12:00:00Z', lastDeployedAt: '2026-10-09T11:00:00Z' });
    expect(await screen.findByText('Domain changes apply on the next deploy.')).toBeTruthy();
    pending.unmount();

    renderTab({ domainsChangedAt: '2026-10-09T10:00:00Z', lastDeployedAt: '2026-10-09T11:00:00Z' });
    await screen.findByText('api.example.com/v1');
    expect(screen.queryByText('Domain changes apply on the next deploy.')).toBeNull();
  });

  it('keeps the rows on screen while the refetch after a save is pending', async () => {
    const refetch = deferred<ComposeDomains>();
    panel.loadDomains.mockResolvedValueOnce(withDomain).mockReturnValueOnce(refetch.promise);
    panel.updateDomain.mockResolvedValue(domain);
    const user = userEvent.setup();
    const { container } = renderTab();

    await user.click(await screen.findByRole('button', { name: 'Edit api.example.com/v1' }));
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(panel.loadDomains).toHaveBeenCalledTimes(2));
    expect(panel.updateDomain).toHaveBeenCalledWith(7, 3, expect.objectContaining({ host: 'api.example.com' }));
    expect(screen.getByRole('link', { name: 'api.example.com/v1' })).toBeTruthy();
    expect(container.querySelector('.ant-skeleton')).toBeNull();

    refetch.resolve({ domains: [{ ...domain, port: 4000 }], services: ['db', 'web'] });
    expect(await screen.findByText('web:4000')).toBeTruthy();
  });

  it('deletes through a Popconfirm', async () => {
    panel.loadDomains.mockResolvedValueOnce(withDomain).mockResolvedValueOnce({ domains: [], services: ['web'] });
    panel.deleteDomain.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderTab();

    await user.click(await screen.findByRole('button', { name: 'Delete api.example.com/v1' }));
    expect(await screen.findByText('Delete api.example.com/v1?')).toBeTruthy();
    expect(panel.deleteDomain).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(panel.deleteDomain).toHaveBeenCalledWith(7, 3));
    expect(await screen.findByText('No domains. Add one to route a Service through Traefik.')).toBeTruthy();
  });

  it('keeps the last good table and shows the error when the refetch after a delete fails', async () => {
    panel.loadDomains.mockResolvedValueOnce(withDomain).mockRejectedValueOnce(new Error('network down'));
    panel.deleteDomain.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderTab();

    await user.click(await screen.findByRole('button', { name: 'Delete api.example.com/v1' }));
    await user.click(await screen.findByRole('button', { name: 'Delete' }));

    expect(await screen.findByText('network down')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'api.example.com/v1' })).toBeTruthy();
    expect(screen.queryByText('Could not load this Compose’s domains')).toBeNull();
  });

  it('shows the full error state with Retry when the first load fails', async () => {
    panel.loadDomains.mockRejectedValueOnce(new Error('boom')).mockResolvedValueOnce(withDomain);
    const user = userEvent.setup();
    renderTab();

    expect(await screen.findByText('Could not load this Compose’s domains')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('api.example.com/v1')).toBeTruthy();
  });

  it('shows the skeleton again when the Compose changes', async () => {
    const second = deferred<ComposeDomains>();
    panel.loadDomains.mockResolvedValueOnce(withDomain).mockReturnValueOnce(second.promise);
    const { container, rerender } = renderTab();
    await screen.findByText('api.example.com/v1');

    rerender(<ComposeDomainsTab compose={{ ...compose, id: 8 }} deploying={false} onDeploy={() => undefined} onOpenEditor={() => undefined} onReloadZeroDowntime={async () => undefined} />);

    expect(container.querySelector('.ant-skeleton')).not.toBeNull();
    expect(screen.queryByText('api.example.com/v1')).toBeNull();
    second.resolve({ domains: [], services: [] });
    expect(await screen.findByText('No domains. Add one to route a Service through Traefik.')).toBeTruthy();
  });
});
