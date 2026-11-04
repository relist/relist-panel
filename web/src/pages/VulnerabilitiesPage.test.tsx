// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { MemoryRouter } from 'react-router';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../data/api';
import type { VulnImage, VulnSettingsResponse, VulnStatus } from '../data/types';
import { VulnerabilitiesPage } from './VulnerabilitiesPage';

const api = vi.hoisted(() => ({
  saveVulnSettings: vi.fn(),
  vulnCves: vi.fn(),
  vulnDeleteCache: vi.fn(),
  vulnFindings: vi.fn(),
  vulnImages: vi.fn(),
  vulnScan: vi.fn(),
  vulnScannerTags: vi.fn(),
  vulnSettings: vi.fn(),
  vulnStatus: vi.fn(),
  vulnUpdateDB: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

vi.mock('../data/store', () => ({
  usePanel: () => ({
    projects: [{ id: 1, name: 'Shop', environments: [{ id: 2, name: 'prod', composes: [{ id: 3, name: 'web' }] }] }],
  }),
}));

const DEFAULT_IMAGE = 'aquasec/trivy:0.76.1@sha256:' + '7'.repeat(64);

const status = (over: Partial<VulnStatus> = {}): VulnStatus => ({
  enabled: true,
  running: null,
  queued: 0,
  db: { version: '2026-10-27T13:12:49Z', updatedAt: '2026-10-27T17:27:02Z', stale: false, error: '' },
  lastDailyAt: null,
  nextDailyAt: '2026-10-29T03:00:00Z',
  timeZone: 'UTC (UTC+00:00)',
  cacheBytes: 1_500_000_000,
  ...over,
});

const settings = (over: Partial<VulnSettingsResponse> = {}): VulnSettingsResponse => ({
  enabled: true,
  afterDeploy: true,
  daily: true,
  dailyTime: '03:00',
  scannerImage: '',
  defaultScannerImage: DEFAULT_IMAGE,
  activeImage: DEFAULT_IMAGE,
  ...over,
});

const appImage: VulnImage = {
  imageId: 'sha256:' + 'a'.repeat(64),
  refs: ['shop-web:latest'],
  users: [{ kind: 'service', projectId: 1, environmentId: 2, composeId: 3, service: 'api' }],
  os: 'alpine 3.22.1',
  counts: { critical: 2, high: 7, medium: 1, low: 0, unknown: 0, fixable: 4 },
  scannedAt: '2026-10-27T18:00:00Z',
  attemptedAt: '2026-10-27T18:00:00Z',
  error: '',
};

function show(path = '/vulnerabilities') {
  return render(
    <SWRConfig value={{ provider: () => new Map(), revalidateOnFocus: false, shouldRetryOnError: false }}>
      <App>
        <MemoryRouter initialEntries={[path]}>
          <VulnerabilitiesPage />
        </MemoryRouter>
      </App>
    </SWRConfig>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  api.vulnStatus.mockResolvedValue(status());
  api.vulnSettings.mockResolvedValue(settings());
  api.vulnImages.mockResolvedValue([appImage]);
  api.vulnCves.mockResolvedValue({ items: [], total: 0 });
  api.vulnFindings.mockResolvedValue([]);
});

describe('VulnerabilitiesPage', () => {
  it('shows Scanning is off with a way to the settings', async () => {
    api.vulnStatus.mockResolvedValue(status({ enabled: false }));
    api.vulnSettings.mockResolvedValue(settings({ enabled: false }));
    show();
    expect(await screen.findByText('Scanning is off')).toBeTruthy();
    expect(screen.getByRole('button', { name: /Scan now/ }).hasAttribute('disabled')).toBe(true);
    await userEvent.setup().click(screen.getByRole('button', { name: /Open settings/ }));
    expect(await screen.findByText('Vulnerability scan settings')).toBeTruthy();
  });

  it('lists images with their users and counts', async () => {
    show();
    expect(await screen.findByText('shop-web:latest')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'api (Shop / prod / web)' }).getAttribute('href')).toBe('/projects/1/env/2/compose/3');
    expect(screen.getByText('2 Critical')).toBeTruthy();
    expect(screen.getByText('7 High')).toBeTruthy();
  });

  it('expands an image to its findings', async () => {
    api.vulnFindings.mockResolvedValue([
      {
        vulnId: 'CVE-2026-1001', pkgName: 'busybox', pkgPath: '', installedVersion: '1.37.0-r18', fixedVersion: '1.37.0-r19',
        severity: 'HIGH', title: 'busybox: overflow', url: 'https://avd.aquasec.com/nvd/cve-2026-1001', firstSeenAt: '2026-10-27T18:00:00Z', new: true,
      },
    ]);
    show();
    await screen.findByText('shop-web:latest');
    await userEvent.setup().click(screen.getByRole('button', { name: /expand row/i }));
    const link = await screen.findByRole('link', { name: 'CVE-2026-1001' });
    expect(link.getAttribute('href')).toBe('https://avd.aquasec.com/nvd/cve-2026-1001');
    expect(screen.getByText('1.37.0-r18 → 1.37.0-r19')).toBeTruthy();
    expect(api.vulnFindings).toHaveBeenCalledWith(appImage.imageId);
  });

  it('copies an image report for an AI agent', async () => {
    api.vulnFindings.mockResolvedValue([
      {
        vulnId: 'CVE-2026-1001', pkgName: 'busybox', pkgPath: '', installedVersion: '1.37.0-r18', fixedVersion: '1.37.0-r19',
        severity: 'HIGH', title: 'busybox: overflow', url: '', firstSeenAt: '2026-10-27T18:00:00Z', new: true,
      },
    ]);
    const user = userEvent.setup();
    show();
    await screen.findByText('shop-web:latest');
    await user.click(screen.getByRole('button', { name: /Export report/ }));
    expect(await screen.findByText('Copied the vulnerability report')).toBeTruthy();
    const copied = await navigator.clipboard.readText();
    expect(copied).toMatch(/^# Vulnerability report — shop-web:latest\n/);
    expect(copied).toContain('| busybox | (os) | 1.37.0-r18 | 1.37.0-r19 | HIGH | CVE-2026-1001 |');
  });

  it('downloads the report instead when the clipboard is missing', async () => {
    const user = userEvent.setup();
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined });
    const createObjectURL = vi.fn(() => 'blob:report');
    Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() });
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined);
    show();
    await screen.findByText('shop-web:latest');
    await user.click(screen.getByRole('button', { name: /Export report/ }));
    expect(await screen.findByText('Copying needs HTTPS, so the report was downloaded instead')).toBeTruthy();
    expect(createObjectURL).toHaveBeenCalled();
    expect((click.mock.contexts[0] as HTMLAnchorElement).download).toBe('vuln-report-shop-web-latest.md');
    click.mockRestore();
  });

  it('cannot export an image that was never scanned', async () => {
    api.vulnImages.mockResolvedValue([{ ...appImage, counts: null, scannedAt: null }]);
    show();
    await screen.findByText('shop-web:latest');
    expect(screen.getByRole('button', { name: /Export report/ }).hasAttribute('disabled')).toBe(true);
  });

  it('filters CVEs from the URL', async () => {
    show('/vulnerabilities?tab=cves&composeId=3&severity=CRITICAL&fixable=1');
    await waitFor(() => expect(api.vulnCves).toHaveBeenCalled());
    expect(api.vulnCves).toHaveBeenLastCalledWith({ composeId: 3, severity: ['CRITICAL'], fixable: true, new: false, q: '', page: 1, pageSize: 20 });
  });

  it('reloads the images when the queue goes idle', async () => {
    api.vulnStatus
      .mockResolvedValueOnce(status({ queued: 1 }))
      .mockResolvedValue(status());
    api.vulnScan.mockResolvedValue({ queued: 1 });
    show();
    await screen.findByText('shop-web:latest');
    await waitFor(() => expect(api.vulnImages).toHaveBeenCalledTimes(1));
    await userEvent.setup().click(screen.getByRole('button', { name: /Scan now/ }));
    await waitFor(() => expect(api.vulnImages).toHaveBeenCalledTimes(2));
  });

  it('shows a failed scan next to its last results', async () => {
    api.vulnImages.mockResolvedValue([{ ...appImage, error: 'scanner ran out of memory (1 GB limit)' }]);
    show();
    expect(await screen.findByText('Last scan failed: scanner ran out of memory (1 GB limit)')).toBeTruthy();
    expect(screen.getByText('2 Critical')).toBeTruthy();
  });

  it('asks before using a young tag', async () => {
    api.vulnScannerTags.mockResolvedValue({
      tags: [{ tag: '0.76.1', digest: 'sha256:' + '1'.repeat(64), ref: 'aquasec/trivy:0.76.1@sha256:' + '1'.repeat(64), pushedAt: '2026-10-21T11:16:41Z', ageDays: 6, young: true }],
    });
    const user = userEvent.setup();
    show();
    await user.click(await screen.findByRole('button', { name: /Settings/ }));
    await user.click(await screen.findByRole('button', { name: /Pick from recent tags/ }));
    const dialog = (await screen.findByText('Recent scanner tags')).closest('.ant-modal') as HTMLElement;
    await user.click(await within(dialog).findByRole('button', { name: 'Use' }));
    expect((await screen.findAllByText('This tag is less than 14 days old. Use it anyway?')).length).toBeGreaterThan(0);
    await user.click(screen.getByRole('button', { name: 'Use anyway' }));
    await waitFor(() =>
      expect((screen.getByLabelText('Scanner image') as HTMLInputElement).value).toBe('aquasec/trivy:0.76.1@sha256:' + '1'.repeat(64)),
    );
  });

  it('shows a save error under the image field', async () => {
    api.saveVulnSettings.mockRejectedValue(new ApiError(422, 'manifest unknown'));
    const user = userEvent.setup();
    show();
    await user.click(await screen.findByRole('button', { name: /Settings/ }));
    const input = await screen.findByLabelText('Scanner image');
    await user.type(input, 'aquasec/trivy:9.9.9@sha256:' + '2'.repeat(64));
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('manifest unknown')).toBeTruthy();
    expect(api.saveVulnSettings).toHaveBeenCalledWith({
      enabled: true, afterDeploy: true, daily: true, dailyTime: '03:00', scannerImage: 'aquasec/trivy:9.9.9@sha256:' + '2'.repeat(64),
    });
  });
  it('queues a DB update from the settings', async () => {
    api.vulnUpdateDB.mockResolvedValue(undefined);
    const user = userEvent.setup();
    show();
    await user.click(await screen.findByRole('button', { name: /Settings/ }));
    await user.click(await screen.findByRole('button', { name: /Update vulnerability DB now/ }));
    expect(await screen.findByText('Queued a vulnerability DB update')).toBeTruthy();
    expect(api.vulnUpdateDB).toHaveBeenCalledTimes(1);
  });

  it('says scans fail when no DB was ever downloaded', async () => {
    api.vulnStatus.mockResolvedValue(status({ db: { version: '', updatedAt: null, stale: true, error: 'Fatal error: boom' } }));
    show();
    expect(await screen.findByText('The last vulnerability DB update failed: Fatal error: boom')).toBeTruthy();
    expect(screen.getByText('Scans fail until a DB is downloaded. The next scan retries the download.')).toBeTruthy();
  });
});
