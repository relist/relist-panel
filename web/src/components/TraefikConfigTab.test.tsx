// @vitest-environment jsdom
import '../test/dom-setup';

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { ConfigPreview, TraefikConfig } from '../data/types';
import { TraefikConfigTab } from './TraefikConfigTab';

const api = vi.hoisted(() => ({
  previewTraefikConfig: vi.fn(),
  restartTraefik: vi.fn(),
  restoreTraefikConfig: vi.fn(),
  testTraefikConfig: vi.fn(),
  traefikConfigHistory: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

// The editor is Monaco; a textarea stands in for it.
vi.mock('./YamlEditor', () => ({
  YamlEditor: ({ onChange, path, readOnly, value }: { onChange?: (v: string) => void; path: string; readOnly?: boolean; value: string }) => (
    <textarea aria-label={path} onChange={(e) => onChange?.(e.target.value)} readOnly={readOnly} value={value} />
  ),
}));

const form = { accessLog: false, acmeEmail: 'ops@example.com', logLevel: 'INFO' as const, readTimeout: '3600s', trustCloudflare: false, trustedIps: [] };

function config(overrides: Partial<TraefikConfig> = {}): TraefikConfig {
  return {
    base: 'log:\n  level: INFO\n',
    effective: '# Generated\n',
    form,
    lastApply: null,
    override: '# mine\n',
    pendingRestart: false,
    revision: 'rev1',
    warnings: [],
    ...overrides,
  };
}

function preview(overrides: Partial<ConfigPreview> = {}): ConfigPreview {
  return { effective: '# Generated\n', errors: [], form, override: '# mine\n', warnings: [], ...overrides };
}

const onReviewApply = vi.fn();
const onStarted = vi.fn();

function show(c = config()) {
  return render(
    <SWRConfig value={{ provider: () => new Map(), revalidateOnFocus: false, shouldRetryOnError: false }}>
      <TraefikConfigTab config={c} onReviewApply={onReviewApply} onStarted={onStarted} />
    </SWRConfig>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  api.traefikConfigHistory.mockResolvedValue([]);
});

describe('TraefikConfigTab', () => {
  it('sends a form edit through preview and marks the draft unsaved', async () => {
    api.previewTraefikConfig.mockResolvedValue(
      preview({ form: { ...form, acmeEmail: 'new@example.com' }, override: '# mine\nemail: new@example.com\n' }),
    );
    show();
    const email = screen.getByRole('textbox', { name: 'ACME email' });
    fireEvent.change(email, { target: { value: 'new@example.com' } });
    await waitFor(() =>
      expect(api.previewTraefikConfig).toHaveBeenCalledWith({
        form: { ...form, acmeEmail: 'new@example.com' },
        override: '# mine\n',
      }),
    );
    expect(await screen.findByText('Unsaved changes')).toBeTruthy();
  });

  it('explains the read timeout and sends it through preview', async () => {
    api.previewTraefikConfig.mockResolvedValue(preview({ form: { ...form, readTimeout: '10m' } }));
    show();
    expect(
      screen.getByText(
        'Longest time to receive one request, body included. Image uploads on the Images page and pushes to the Registry need minutes per large layer.',
      ),
    ).toBeTruthy();
    fireEvent.change(screen.getByRole('textbox', { name: 'Request read timeout' }), { target: { value: '10m' } });
    await waitFor(() =>
      expect(api.previewTraefikConfig).toHaveBeenCalledWith({ form: { ...form, readTimeout: '10m' }, override: '# mine\n' }),
    );
    expect(screen.queryByText('Large image uploads and registry pushes may be cut off.')).toBeNull();
  });

  it("saves base's read timeout when the field is cleared", async () => {
    api.previewTraefikConfig.mockResolvedValue(preview({ form: { ...form, readTimeout: '5m' } }));
    show(config({ form: { ...form, readTimeout: '5m' } }));
    const field = screen.getByRole('textbox', { name: 'Request read timeout' });
    fireEvent.change(field, { target: { value: '' } });
    await waitFor(() =>
      expect(api.previewTraefikConfig).toHaveBeenCalledWith({ form: { ...form, readTimeout: '3600s' }, override: '# mine\n' }),
    );
    expect((field as HTMLInputElement).value).toBe('');
  });

  it('warns about a read timeout under ten minutes', () => {
    show();
    fireEvent.change(screen.getByRole('textbox', { name: 'Request read timeout' }), { target: { value: '5m' } });
    expect(screen.getByText('Large image uploads and registry pushes may be cut off.')).toBeTruthy();
  });

  it('keeps the read timeout as typed when preview rejects it', async () => {
    api.previewTraefikConfig.mockResolvedValue(
      preview({
        errors: [{ key: 'entryPoints.websecure.transport.respondingTimeouts.readTimeout', line: 0, message: 'read timeout must be a duration of at least 30s, like 90s, 5m or 1h' }],
        form,
      }),
    );
    show();
    const field = screen.getByRole('textbox', { name: 'Request read timeout' });
    fireEvent.change(field, { target: { value: '1' } });
    await waitFor(() => expect(api.previewTraefikConfig).toHaveBeenCalled());
    await waitFor(() => expect(screen.getByText('read timeout must be a duration of at least 30s, like 90s, 5m or 1h')).toBeTruthy());
    expect((field as HTMLInputElement).value).toBe('1');
  });

  it('disables Settings while the YAML does not parse', async () => {
    const user = userEvent.setup();
    api.previewTraefikConfig.mockResolvedValue(
      preview({ effective: '', errors: [{ key: '', line: 1, message: 'yaml: line 1: oops' }], form: null, override: 'a: [oops\n' }),
    );
    show();
    await user.click(screen.getByText('Advanced'));
    fireEvent.change(screen.getByRole('textbox', { name: 'traefik.override.yml' }), { target: { value: 'a: [oops\n' } });
    expect(await screen.findByText(/Line 1: yaml: line 1: oops/)).toBeTruthy();
    await user.click(screen.getByText('Settings'));
    expect(await screen.findByText('Fix the YAML in Advanced first')).toBeTruthy();
    expect((screen.getByRole('textbox', { name: 'ACME email' }) as HTMLInputElement).disabled).toBe(true);
  });

  it('discards the draft back to the saved override', async () => {
    const user = userEvent.setup();
    api.previewTraefikConfig.mockResolvedValue(preview({ override: 'log: {level: DEBUG}\n' }));
    show();
    await user.click(screen.getByText('Advanced'));
    const editor = screen.getByRole('textbox', { name: 'traefik.override.yml' }) as HTMLTextAreaElement;
    fireEvent.change(editor, { target: { value: 'log: {level: DEBUG}\n' } });
    await user.click(await screen.findByRole('button', { name: 'Discard' }));
    expect((screen.getByRole('textbox', { name: 'traefik.override.yml' }) as HTMLTextAreaElement).value).toBe('# mine\n');
    expect(screen.queryByText('Unsaved changes')).toBeNull();
  });

  it('shows Trust Cloudflare as the form says', () => {
    show(config({ form: { ...form, trustCloudflare: true } }));
    expect(screen.getByRole('switch', { name: 'Trust Cloudflare' }).getAttribute('aria-checked')).toBe('true');
  });

  it('lists history and restores an entry', async () => {
    const user = userEvent.setup();
    api.traefikConfigHistory.mockResolvedValue([
      { at: '2026-11-04T10:00:00Z', kind: 'apply', name: '20261104T100000Z-abcdefgh', result: 'ok' },
    ]);
    api.restoreTraefikConfig.mockResolvedValue({ id: 'run1' });
    show();
    await user.click(screen.getByText('History'));
    await user.click(await screen.findByRole('button', { name: 'Restore 20261104T100000Z-abcdefgh' }));
    await user.click(await screen.findByRole('button', { name: 'Restore' }));
    await waitFor(() => expect(api.restoreTraefikConfig).toHaveBeenCalledWith('20261104T100000Z-abcdefgh'));
    expect(onStarted).toHaveBeenCalledWith('run1');
  });

  it('warns when web and websecure trust different proxies', () => {
    show(config({ warnings: [{ key: 'entryPoints.web.forwardedHeaders.trustedIPs', line: 0, message: 'web and websecure differ; saving here makes them the same' }] }));
    expect(screen.getByText('web and websecure differ; saving here makes them the same')).toBeTruthy();
  });

  it('hands the draft and its generated file to Review & Apply', async () => {
    const user = userEvent.setup();
    api.previewTraefikConfig.mockResolvedValue(preview({ effective: '# new\n', override: 'log: {level: DEBUG}\n' }));
    show();
    await user.click(screen.getByText('Advanced'));
    fireEvent.change(screen.getByRole('textbox', { name: 'traefik.override.yml' }), { target: { value: 'log: {level: DEBUG}\n' } });
    await waitFor(() => expect(api.previewTraefikConfig).toHaveBeenCalled());
    const review = screen.getByRole('button', { name: 'Review & Apply' }) as HTMLButtonElement;
    await waitFor(() => expect(review.disabled).toBe(false));
    await user.click(review);
    expect(onReviewApply).toHaveBeenCalledWith('log: {level: DEBUG}\n', '# new\n');
  });

  it('offers a Restart when the file changed since Traefik started', async () => {
    const user = userEvent.setup();
    api.restartTraefik.mockResolvedValue({ id: 'run2' });
    show(config({ pendingRestart: true }));
    expect(screen.getByText('Config changed since Traefik started — Restart to apply')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Restart' }));
    await screen.findByText('Restart Traefik?');
    await user.click(screen.getAllByRole('button', { name: 'Restart' })[1]);
    await waitFor(() => expect(api.restartTraefik).toHaveBeenCalled());
    expect(onStarted).toHaveBeenCalledWith('run2');
  });

  it('tests the draft in a throwaway Traefik and shows what it said', async () => {
    const user = userEvent.setup();
    api.testTraefikConfig.mockResolvedValue({ errors: [], log: 'ERR field not found, node: bogus', ok: false });
    show();
    await user.click(screen.getByRole('button', { name: 'Test' }));
    await waitFor(() => expect(api.testTraefikConfig).toHaveBeenCalledWith('# mine\n'));
    expect(await screen.findByText('Traefik did not start with this config.')).toBeTruthy();
    expect(screen.getByText(/field not found, node: bogus/)).toBeTruthy();
  });

  it('says when the test passed', async () => {
    const user = userEvent.setup();
    api.testTraefikConfig.mockResolvedValue({ errors: [], log: '', ok: true });
    show();
    await user.click(screen.getByRole('button', { name: 'Test' }));
    expect(await screen.findByText('Traefik started with this config.')).toBeTruthy();
  });
});
