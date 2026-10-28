// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '../data/api';
import type { Domain } from '../data/types';
import { DomainFormModal } from './DomainFormModal';

const RESOLVER_HELP =
  "Must match a certificate resolver in Traefik's static config. The panel can't check it — a wrong name shows only in Traefik's log.";

const domain: Domain = {
  certProvider: 'custom',
  certResolver: 'corp-dns',
  createdAt: '2026-10-09T10:00:00Z',
  entrypoint: 'web-internal',
  host: 'api.example.com',
  https: true,
  id: 3,
  internalPath: '/v1',
  middlewares: ['rate-limit@file'],
  path: '/api',
  port: 3000,
  service: 'web',
  stripPath: true,
  updatedAt: '2026-10-09T10:00:00Z',
};

function modalElement(props: { domain?: Domain; error?: ApiError; onSubmit?: () => void } = {}) {
  return (
    <DomainFormModal
      domain={props.domain}
      error={props.error}
      onCancel={() => undefined}
      onSubmit={props.onSubmit ?? (() => undefined)}
      open
      saving={false}
      services={['db', 'web']}
    />
  );
}

function renderModal(props: { domain?: Domain; error?: ApiError } = {}) {
  const onSubmit = vi.fn();
  const view = render(modalElement({ ...props, onSubmit }));
  /** The server's refusal arrives after the modal is open, as after a submit. */
  const fail = (error: ApiError) => view.rerender(modalElement({ ...props, error, onSubmit }));
  return { fail, onSubmit };
}

/** Opens a Select by its label and clicks the option with that text. */
async function choose(user: ReturnType<typeof userEvent.setup>, label: string, option: string) {
  await user.click(screen.getByRole('combobox', { name: label }));
  await user.click(await screen.findByTitle(option));
}

const NONE_HELP =
  'Traefik serves an uploaded certificate (Certificates page) that covers this host, otherwise its default self-signed certificate.';

describe('DomainFormModal', () => {
  it('hides Certificate Provider until HTTPS is on, then defaults to None', async () => {
    const user = userEvent.setup();
    renderModal();
    await screen.findByRole('dialog');

    expect(screen.queryByText('Certificate Provider')).toBeNull();
    await user.click(screen.getByRole('switch', { name: 'HTTPS' }));

    const provider = await screen.findByRole('combobox', { name: 'Certificate Provider' });
    expect(provider).toBeTruthy();
    expect(screen.getByTitle('None')).toBeTruthy();
    expect(screen.getByText(NONE_HELP)).toBeTruthy();
  });

  it('shows Resolver name with its help only for the Custom provider', async () => {
    const user = userEvent.setup();
    renderModal();
    await user.click(screen.getByRole('switch', { name: 'HTTPS' }));
    expect(screen.queryByText('Resolver name')).toBeNull();

    await choose(user, 'Certificate Provider', 'Custom');
    expect(await screen.findByText('Resolver name')).toBeTruthy();
    expect(screen.getByText(RESOLVER_HELP)).toBeTruthy();

    await choose(user, 'Certificate Provider', 'None');
    await waitFor(() => expect(screen.queryByText('Resolver name')).toBeNull());
    expect(
      screen.getByText(NONE_HELP),
    ).toBeTruthy();
  });

  it('shows the Entrypoint input only when Custom Entrypoint is on', async () => {
    const user = userEvent.setup();
    renderModal();
    expect(screen.queryByRole('textbox', { name: 'Entrypoint' })).toBeNull();

    await user.click(screen.getByRole('switch', { name: 'Custom Entrypoint' }));
    expect(await screen.findByRole('textbox', { name: 'Entrypoint' })).toBeTruthy();

    await user.click(screen.getByRole('switch', { name: 'Custom Entrypoint' }));
    await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Entrypoint' })).toBeNull());
  });

  it('clears the resolver when the provider is switched from Custom to Let’s Encrypt', async () => {
    const user = userEvent.setup();
    const { onSubmit } = renderModal({ domain });

    expect(await screen.findByDisplayValue('corp-dns')).toBeTruthy();
    await choose(user, 'Certificate Provider', "Let's Encrypt");
    await user.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ certProvider: 'letsencrypt', certResolver: '', https: true }),
    );
  });

  it('shows a server error that names the host on the Host field', async () => {
    const { fail } = renderModal();
    const input = await screen.findByRole('textbox', { name: 'Host' });
    fail(new ApiError(400, 'host is not a valid hostname', 'host'));
    const message = await screen.findByText('host is not a valid hostname');
    expect(message.closest('.ant-form-item')?.contains(input)).toBe(true);
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('shows a 409 above the form', async () => {
    const { fail } = renderModal();
    await screen.findByRole('dialog');
    fail(new ApiError(409, 'api.example.com/ is already used by my-saas / production / web'));

    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('api.example.com/ is already used by my-saas / production / web');
    expect(document.querySelector('.ant-form-item-has-error')).toBeNull();
  });

  it('pre-fills Edit mode from the domain', async () => {
    renderModal({ domain });

    expect(await screen.findByText('Edit domain')).toBeTruthy();
    expect(await screen.findByRole('textbox', { name: 'Resolver name' })).toBeTruthy();
    expect((screen.getByRole('textbox', { name: 'Host' }) as HTMLInputElement).value).toBe('api.example.com');
    expect((screen.getByRole('textbox', { name: 'Path' }) as HTMLInputElement).value).toBe('/api');
    expect((screen.getByRole('textbox', { name: 'Internal Path' }) as HTMLInputElement).value).toBe('/v1');
    expect(screen.getByRole('switch', { name: 'Strip Path' }).getAttribute('aria-checked')).toBe('true');
    expect(screen.getByRole('switch', { name: 'HTTPS' }).getAttribute('aria-checked')).toBe('true');
    expect(screen.getByRole('spinbutton', { name: 'Container Port' }).getAttribute('aria-valuenow')).toBe('3000');
    expect((screen.getByRole('textbox', { name: 'Entrypoint' }) as HTMLInputElement).value).toBe('web-internal');
    expect((screen.getByRole('textbox', { name: 'Resolver name' }) as HTMLInputElement).value).toBe('corp-dns');
    expect(screen.getByText('rate-limit@file')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Save' })).toBeTruthy();
  });
});
