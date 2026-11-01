// @vitest-environment jsdom
import '../test/dom-setup';

import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../data/api';
import type { PanelUpdateRun, PanelUpdateStatus, PreflightReport } from '../data/panel-update';
import { PanelUpdatePage, UPDATED_FLAG } from './PanelUpdatePage';

const api = vi.hoisted(() => ({
  panelUpdateApply: vi.fn(),
  panelUpdateCheck: vi.fn(),
  panelUpdatePreflight: vi.fn(),
  panelUpdateSettings: vi.fn(),
  panelUpdateStatus: vi.fn(),
}));
const navigation = vi.hoisted(() => ({ reload: vi.fn(), toSignIn: vi.fn() }));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));
vi.mock('../data/panel-update', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/panel-update')>()),
  navigation,
}));
// Monaco cannot run in jsdom; the stand-in shows what the editor would get.
vi.mock('../components/DiffViewer', () => ({
  DiffViewer: ({ modified, original }: { modified: string; original: string }) => (
    <div data-testid="diff">
      <pre data-testid="diff-original">{original}</pre>
      <pre data-testid="diff-modified">{modified}</pre>
    </div>
  ),
}));

const run = (over: Partial<PanelUpdateRun> = {}): PanelUpdateRun => ({
  id: 'r1',
  state: 'waiting-health',
  terminal: false,
  error: '',
  rollbackError: '',
  fromVersion: 'v1.0.0',
  toVersion: 'v1.1.0',
  startedAt: 1,
  backupPath: '/var/lib/relist-panel/.panel-update/r1',
  ...over,
});

const status = (over: Partial<PanelUpdateStatus> = {}): PanelUpdateStatus => ({
  version: 'v1.0.0',
  development: false,
  available: null,
  checkEnabled: true,
  lastCheckAt: '2026-11-01T00:00:00Z',
  lastError: '',
  install: { available: true, reason: '' },
  locked: false,
  run: null,
  ...over,
});

const available = {
  version: 'v1.1.0',
  releasedAt: '2026-10-30T00:00:00Z',
  notesUrl: 'https://example.com/notes',
  manualSteps: [],
};

const report = (over: Partial<PreflightReport> = {}): PreflightReport => ({
  id: 'rep1',
  createdAt: '2026-11-01T00:00:00Z',
  fromVersion: 'v1.0.0',
  toVersion: 'v1.1.0',
  notesUrl: 'https://example.com/notes',
  ready: true,
  failed: null,
  needsManualUpdate: false,
  unknownKinds: [],
  readmeUrl: 'https://example.com/readme',
  gate: { installAvailable: true, migrationLocked: false, deploysBusy: false, updateRunning: false },
  change: 'not-edited',
  diff: '',
  conflict: null,
  invariants: [{ kind: 'network', text: 'The panel joins relist-network', ok: true }],
  recreated: ['panel'],
  traefikRecreated: false,
  manualSteps: [],
  ...over,
});

function show() {
  return render(
    <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
      <App>
        <PanelUpdatePage />
      </App>
    </SWRConfig>,
  );
}

const tick = (ms: number) => act(async () => { await vi.advanceTimersByTimeAsync(ms); });

beforeEach(() => {
  Object.values(api).forEach((fn) => fn.mockReset());
  Object.values(navigation).forEach((fn) => fn.mockReset());
  sessionStorage.clear();
});
afterEach(() => {
  vi.useRealTimers();
});

describe('PanelUpdatePage states', () => {
  it('shows Up to date with the version, last check and the daily switch', async () => {
    api.panelUpdateStatus.mockResolvedValue(status());
    show();
    expect(await screen.findByText('This panel is up to date.')).toBeTruthy();
    expect(screen.getByText('v1.0.0')).toBeTruthy();
    expect(screen.getByRole('switch', { name: 'Check for an update every day' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Check now' })).toBeTruthy();
  });

  it('shows Update available with a Preview button', async () => {
    api.panelUpdateStatus.mockResolvedValue(status({ available }));
    show();
    expect(await screen.findByText('Version v1.1.0 is available')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Preview' })).toBeTruthy();
  });

  it('shows Needs a manual update when the Install is unavailable', async () => {
    api.panelUpdateStatus.mockResolvedValue(
      status({ available, install: { available: false, reason: 'The panel Service uses build:' } }),
    );
    show();
    expect(await screen.findByText('This Install needs a manual update')).toBeTruthy();
    expect(screen.getByText('The panel Service uses build:')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Preview' })).toBeNull();
  });

  it('shows Needs a manual update for unknown invariant kinds, with the README link', async () => {
    api.panelUpdateStatus.mockResolvedValue(status({ available }));
    api.panelUpdatePreflight.mockResolvedValue(report({ needsManualUpdate: true, unknownKinds: ['gpu'], ready: false }));
    show();
    await userEvent.click(await screen.findByRole('button', { name: 'Preview' }));
    expect(await screen.findByText('This Install needs a manual update')).toBeTruthy();
    expect(screen.getByText(/gpu/)).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Upgrading an existing install' }).getAttribute('href')).toBe('https://example.com/readme');
  });

  it('shows Preview and enables Update only once every manual step is ticked, then confirms', async () => {
    api.panelUpdateStatus.mockResolvedValue(status({ available }));
    api.panelUpdatePreflight.mockResolvedValue(
      report({ manualSteps: [{ id: 'dns', text: 'Point DNS at the host' }], recreated: ['panel', 'traefik'], traefikRecreated: true }),
    );
    api.panelUpdateApply.mockResolvedValue({ id: 'r1' });
    show();
    await userEvent.click(await screen.findByRole('button', { name: 'Preview' }));
    const update = await screen.findByRole('button', { name: 'Update' });
    expect((update as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText('The panel joins relist-network')).toBeTruthy();
    expect(screen.getAllByText(/Traefik is recreated/).length).toBe(1);

    await userEvent.click(screen.getByRole('checkbox', { name: 'Point DNS at the host' }));
    expect((screen.getByRole('button', { name: 'Update' }) as HTMLButtonElement).disabled).toBe(false);
    await userEvent.click(screen.getByRole('button', { name: 'Update' }));

    expect(await screen.findByText(/Queued Deployments are cancelled and new ones are refused until the update ends/)).toBeTruthy();
    expect(screen.getAllByText(/Traefik is recreated/).length).toBe(2);
  });

  it('applies the report id and the ticked manual steps from the confirm', async () => {
    api.panelUpdateStatus.mockResolvedValue(status({ available }));
    api.panelUpdatePreflight.mockResolvedValue(report());
    api.panelUpdateApply.mockResolvedValue({ id: 'r1' });
    show();
    await userEvent.click(await screen.findByRole('button', { name: 'Preview' }));
    await userEvent.click(await screen.findByRole('button', { name: 'Update' }));
    const dialog = await screen.findByRole('dialog');
    const ok = Array.from(dialog.querySelectorAll('button')).find((b) => b.textContent === 'Update')!;
    await userEvent.click(ok);
    await waitFor(() => expect(api.panelUpdateApply).toHaveBeenCalledWith('rep1', []));
    expect(await screen.findByText('Updating')).toBeTruthy();
  });

  it('shows Refused with the server words, the conflict diff and the invariant list', async () => {
    api.panelUpdateStatus.mockResolvedValue(status({ available }));
    api.panelUpdatePreflight.mockResolvedValue(
      report({
        ready: false,
        failed: { step: 'change', message: 'The Install file was edited and the edits do not merge.' },
        change: 'edited-conflict',
        conflict: { ours: 'ours text', theirs: 'theirs text', merged: '<<<<<<<', hunks: 2 },
        invariants: [
          { kind: 'network', text: 'The panel joins relist-network', ok: false, detail: 'network missing', source: 'docker-compose.override.yml' },
        ],
      }),
    );
    show();
    await userEvent.click(await screen.findByRole('button', { name: 'Preview' }));
    expect(await screen.findByText('The Install file was edited and the edits do not merge.')).toBeTruthy();
    expect(screen.getByTestId('diff-original').textContent).toBe('ours text');
    expect(screen.getByTestId('diff-modified').textContent).toBe('theirs text');
    expect(screen.getByText('network missing')).toBeTruthy();
    expect(screen.getByText('docker-compose.override.yml')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Update' })).toBeNull();
  });

  it('shows the merge diff of an edited Install file', async () => {
    api.panelUpdateStatus.mockResolvedValue(status({ available }));
    api.panelUpdatePreflight.mockResolvedValue(
      report({ change: 'edited-merged', diff: '--- current\n+++ new\n@@ -1,2 +1,2 @@\n a\n-b\n+c\n' }),
    );
    show();
    await userEvent.click(await screen.findByRole('button', { name: 'Preview' }));
    await screen.findByTestId('diff');
    expect(screen.getByTestId('diff-original').textContent).toBe('a\nb');
    expect(screen.getByTestId('diff-modified').textContent).toBe('a\nc');
  });

  it('shows Updating with the run steps', async () => {
    api.panelUpdateStatus.mockResolvedValue(status({ available, locked: true, run: run({ state: 'backing-up' }) }));
    show();
    expect(await screen.findByText('Updating to v1.1.0')).toBeTruthy();
    expect(screen.getByText('Back up the database and the Install files')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Check now' })).toBeNull();
  });
});

describe('PanelUpdatePage polling', () => {
  it('shows Restarting with the elapsed time on a network error, then recovers', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    api.panelUpdateStatus
      .mockResolvedValueOnce(status({ run: run() }))
      .mockRejectedValueOnce(new TypeError('Failed to fetch'))
      .mockResolvedValue(status({ run: run() }));
    show();
    await screen.findByText('Updating to v1.1.0');
    await tick(2000);
    expect(await screen.findByText('Restarting…')).toBeTruthy();
    expect(screen.getByText(/Waiting 0:0\d/)).toBeTruthy();
    await tick(2000);
    await waitFor(() => expect(screen.queryByText('Restarting…')).toBeNull());
  });

  it.each([502, 404, 500])('treats a %i as Restarting, never as a failure', async (code) => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    api.panelUpdateStatus
      .mockResolvedValueOnce(status({ run: run() }))
      .mockRejectedValue(new ApiError(code, 'bad gateway'));
    show();
    await screen.findByText('Updating to v1.1.0');
    await tick(2000);
    expect(await screen.findByText('Restarting…')).toBeTruthy();
    expect(screen.queryByText('bad gateway')).toBeNull();
  });

  it('names the host commands after five minutes with no answer', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    api.panelUpdateStatus.mockResolvedValueOnce(status({ run: run() })).mockRejectedValue(new TypeError('Failed to fetch'));
    show();
    await screen.findByText('Updating to v1.1.0');
    await tick(2000);
    await screen.findByText('Restarting…');
    await tick(5 * 60 * 1000);
    expect(await screen.findByText('The panel is not answering')).toBeTruthy();
    expect(screen.getByText(/docker logs relist-panel-updater-r1/)).toBeTruthy();
  });

  it('sends the operator to sign in on a 401', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    api.panelUpdateStatus
      .mockResolvedValueOnce(status({ run: run() }))
      .mockRejectedValue(new ApiError(401, 'authentication required'));
    show();
    await screen.findByText('Updating to v1.1.0');
    await tick(2000);
    expect(navigation.toSignIn).toHaveBeenCalledTimes(1);
    expect(screen.queryByText('Restarting…')).toBeNull();
  });

  it('reloads once when the new version answers and the run succeeded', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    api.panelUpdateStatus
      .mockResolvedValueOnce(status({ run: run() }))
      .mockResolvedValue(status({ version: 'v1.1.0', run: run({ state: 'succeeded', terminal: true }) }));
    show();
    await screen.findByText('Updating to v1.1.0');
    await tick(2000);
    await waitFor(() => expect(navigation.reload).toHaveBeenCalledTimes(1));
    expect(sessionStorage.getItem(UPDATED_FLAG)).toBe('v1.1.0');
    await tick(10_000);
    expect(navigation.reload).toHaveBeenCalledTimes(1);
  });

  it('does not reload a page that opens on an old successful run', async () => {
    api.panelUpdateStatus.mockResolvedValue(status({ version: 'v1.1.0', run: run({ state: 'succeeded', terminal: true }) }));
    show();
    await screen.findByText('This panel is up to date.');
    expect(navigation.reload).not.toHaveBeenCalled();
  });

  it('says the update worked on the page it reloads into', async () => {
    sessionStorage.setItem(UPDATED_FLAG, 'v1.1.0');
    api.panelUpdateStatus.mockResolvedValue(status({ version: 'v1.1.0', run: run({ state: 'succeeded', terminal: true }) }));
    show();
    expect(await screen.findByText('Updated to v1.1.0')).toBeTruthy();
  });
});

describe('PanelUpdatePage finished runs', () => {
  it('shows Rolled back as a warning with the reason', async () => {
    api.panelUpdateStatus.mockResolvedValue(
      status({ run: run({ state: 'rolled-back', terminal: true, error: 'the new panel never became healthy' }) }),
    );
    show();
    expect(await screen.findByText('Rolled back to v1.0.0')).toBeTruthy();
    expect(screen.getByText('the new panel never became healthy')).toBeTruthy();
  });

  it('shows Rollback failed with both errors, the backup path and the restore commands', async () => {
    api.panelUpdateStatus.mockResolvedValue(
      status({ run: run({ state: 'rollback-failed', terminal: true, error: 'health timed out', rollbackError: 'db restore failed' }) }),
    );
    show();
    expect(await screen.findByText('The rollback failed')).toBeTruthy();
    expect(screen.getByText(/health timed out/)).toBeTruthy();
    expect(screen.getByText(/db restore failed/)).toBeTruthy();
    expect(screen.getAllByText(/\/var\/lib\/relist-panel\/\.panel-update\/r1/).length).toBeGreaterThan(0);
    expect(screen.getByText(/docker compose up -d/)).toBeTruthy();
  });

  it('shows Interrupted with the running version, the backup path and the check commands', async () => {
    api.panelUpdateStatus.mockResolvedValue(status({ run: run({ state: 'interrupted', terminal: true }) }));
    show();
    expect(await screen.findByText('The update was interrupted')).toBeTruthy();
    expect(screen.getByText(/The Updater stopped before it finished/)).toBeTruthy();
    expect(screen.getByText(/docker logs relist-panel-updater-r1/)).toBeTruthy();
  });
});
