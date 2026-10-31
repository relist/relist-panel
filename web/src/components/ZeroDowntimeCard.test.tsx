// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ConfigProvider } from 'antd';
import { type ReactNode, useState } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Compose, ZeroDowntimeService } from '../data/types';
import { ZERO_DOWNTIME_REQUIREMENTS } from './ZeroDowntimeRequirements';
import { ZeroDowntimeCard } from './ZeroDowntimeCard';

const panel = vi.hoisted(() => ({
  setZeroDowntime: vi.fn(),
}));

vi.mock('../data/store', () => ({ usePanel: () => panel }));

const compose: Compose = {
  createdAt: '2026-10-09T09:00:00Z',
  domainsChangedAt: null,
  envSlug: '',
  environmentId: 1,
  id: 7,
  lastDeployedAt: null,
  name: 'shop',
  projectSlug: '',
  status: { running: 0, total: 0 },
  updatedAt: '2026-10-09T09:00:00Z',
  yamlContent: '',
};

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, reject, resolve };
}

const onOpenEditor = vi.fn();

/** Without motion a closed Modal leaves the DOM at once; jsdom never ends the animation. */
function still(node: ReactNode) {
  return render(<ConfigProvider theme={{ token: { motion: false } }}>{node}</ConfigProvider>);
}

/** The page's reread of the switches; each test says what it returns. */
const reload = vi.fn<() => Promise<ZeroDowntimeService[]>>();

/** Stands in for ComposePage, which owns the list and rereads it on request. */
function Page({ initial }: { initial: ZeroDowntimeService[] }) {
  const [services, setServices] = useState(initial);
  return (
    <ZeroDowntimeCard
      compose={compose}
      onOpenEditor={onOpenEditor}
      onReload={async () => setServices(await reload())}
      services={services}
    />
  );
}

async function renderCard(rows: ZeroDowntimeService[]) {
  reload.mockResolvedValue(rows);
  still(<Page initial={rows} />);
  return screen.findByRole('switch', { name: 'Zero-downtime deploy for web' });
}

const off: ZeroDowntimeService = { service: 'web', enabled: false, reasons: [] };

beforeEach(() => {
  vi.resetAllMocks();
});

describe('ZeroDowntimeCard', () => {
  it('lists the seven requirements in order', () => {
    expect(ZERO_DOWNTIME_REQUIREMENTS).toHaveLength(7);
    expect(ZERO_DOWNTIME_REQUIREMENTS[0]).toBe('A healthcheck that passes only once the app can serve requests');
    expect(ZERO_DOWNTIME_REQUIREMENTS[6]).toBe(
      'The app tolerates two copies running for a few seconds (no SQLite file shared through a named volume, no migrations that lock out the old version)',
    );
  });

  it('renders nothing while loading or when no Service has a Domain', () => {
    const onReload = vi.fn();
    const { container, rerender } = render(<ZeroDowntimeCard compose={compose} onOpenEditor={onOpenEditor} onReload={onReload} />);
    expect(container.textContent).toBe('');
    rerender(<ZeroDowntimeCard compose={compose} onOpenEditor={onOpenEditor} onReload={onReload} services={[]} />);
    expect(container.textContent).toBe('');
    expect(onReload).not.toHaveBeenCalled();
  });

  it('shows a failed first read with Retry', async () => {
    const onReload = vi.fn(async () => undefined);
    const user = userEvent.setup();
    render(<ZeroDowntimeCard compose={compose} error="Request failed (500)" onOpenEditor={onOpenEditor} onReload={onReload} />);
    expect(screen.getByText('Request failed (500)')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Retry' }));
    expect(onReload).toHaveBeenCalledTimes(1);
  });

  it('shows the help text', async () => {
    await renderCard([off]);
    expect(
      screen.getByText(
        "New containers take traffic only once their healthcheck passes; the old one is then stopped. See the requirements under the YAML editor. Changing this Service's Domains makes the next deploy briefly unavailable.",
      ),
    ).toBeTruthy();
  });

  it('opens the confirm modal on turning on, without a request', async () => {
    const user = userEvent.setup();
    const toggle = await renderCard([off]);
    await user.click(toggle);
    const dialog = await screen.findByRole('dialog', { name: 'Turn on zero-downtime deploy for web?' });
    for (const line of ZERO_DOWNTIME_REQUIREMENTS) {
      expect(within(dialog).getByText(line)).toBeTruthy();
    }
    expect(panel.setZeroDowntime).not.toHaveBeenCalled();
    expect(toggle.getAttribute('aria-checked')).toBe('false');
  });

  it('changes nothing on Cancel or Esc', async () => {
    const user = userEvent.setup();
    const toggle = await renderCard([off]);
    await user.click(toggle);
    await user.click(await screen.findByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await user.click(toggle);
    await screen.findByRole('dialog');
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(panel.setZeroDowntime).not.toHaveBeenCalled();
    expect(toggle.getAttribute('aria-checked')).toBe('false');
  });

  it('turns on after Accept, loading until the PUT returns', async () => {
    const user = userEvent.setup();
    const toggle = await renderCard([off]);
    const put = deferred<void>();
    panel.setZeroDowntime.mockReturnValue(put.promise);
    await user.click(toggle);
    const accept = await screen.findByRole('button', { name: 'Accept and turn on' });
    await user.click(accept);
    expect(panel.setZeroDowntime).toHaveBeenCalledTimes(1);
    expect(panel.setZeroDowntime).toHaveBeenCalledWith(7, 'web', true);
    expect(accept.className).toContain('ant-btn-loading');
    reload.mockResolvedValue([{ ...off, enabled: true }]);
    put.resolve();
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(screen.getByRole('switch', { name: 'Zero-downtime deploy for web' }).getAttribute('aria-checked')).toBe('true'));
  });

  it('cannot be cancelled while the PUT is in flight', async () => {
    const user = userEvent.setup();
    const toggle = await renderCard([off]);
    const put = deferred<void>();
    panel.setZeroDowntime.mockReturnValue(put.promise);
    await user.click(toggle);
    await user.click(await screen.findByRole('button', { name: 'Accept and turn on' }));
    const cancel = screen.getByRole('button', { name: 'Cancel' }) as HTMLButtonElement;
    expect(cancel.disabled).toBe(true);
    await user.keyboard('{Escape}');
    expect(screen.getByRole('dialog')).toBeTruthy();
    put.reject(new Error('web has no domain'));
    expect(await within(screen.getByRole('dialog')).findByText('web has no domain')).toBeTruthy();
  });

  it('keeps the modal open with the error when Accept fails', async () => {
    const user = userEvent.setup();
    const toggle = await renderCard([off]);
    panel.setZeroDowntime.mockRejectedValue(new Error('web has no domain'));
    await user.click(toggle);
    await user.click(await screen.findByRole('button', { name: 'Accept and turn on' }));
    const dialog = screen.getByRole('dialog');
    expect(await within(dialog).findByText('web has no domain')).toBeTruthy();
    expect(toggle.getAttribute('aria-checked')).toBe('false');
  });

  it('warns in the modal with the current reasons', async () => {
    const user = userEvent.setup();
    const toggle = await renderCard([{ ...off, reasons: ['has no healthcheck'] }]);
    await user.click(toggle);
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('Deploys will be refused until these are fixed')).toBeTruthy();
    expect(within(dialog).getByText('has no healthcheck')).toBeTruthy();
  });

  it('turns off at once, with no modal', async () => {
    const user = userEvent.setup();
    const toggle = await renderCard([{ ...off, enabled: true }]);
    panel.setZeroDowntime.mockResolvedValue(undefined);
    await user.click(toggle);
    expect(panel.setZeroDowntime).toHaveBeenCalledWith(7, 'web', false);
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('warns under a switched-on row that has reasons, linking to the editor', async () => {
    const user = userEvent.setup();
    await renderCard([{ ...off, enabled: true, reasons: ['has no healthcheck'] }]);
    expect(screen.getByText('has no healthcheck')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Open editor' }));
    expect(onOpenEditor).toHaveBeenCalledTimes(1);
  });
});
