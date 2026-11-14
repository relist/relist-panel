// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import type { ReactElement } from 'react';
import userEvent from '@testing-library/user-event';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../data/api';
import type { Domain } from '../data/types';
import { DomainFormModal } from './DomainFormModal';

const api = vi.hoisted(() => ({ createAuthGroup: vi.fn(), listAuthGroupOptions: vi.fn() }));
const session = vi.hoisted(() => ({ admin: true }));

vi.mock('../data/api', async (importOriginal) => ({ ...(await importOriginal<typeof import('../data/api')>()), api }));
vi.mock('../data/useCan', () => ({ useIsAdmin: () => session.admin }));

beforeEach(() => {
  vi.resetAllMocks();
  session.admin = true;
  api.listAuthGroupOptions.mockResolvedValue([
    { id: 4, name: 'Staging' },
    { id: 8, name: 'Partners' },
  ]);
});

const AUTH_HELP =
  'Visitors must sign in with a user of this group. Applies on the next deploy; edits to the group apply immediately.';

const RESOLVER_HELP =
  "Must match a certificate resolver in Traefik's static config. The panel can't check it — a wrong name shows only in Traefik's log.";

const domain: Domain = {
  authGroupId: null,
  authGroupName: '',
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
  const wrap = (node: ReactElement) => (
    <SWRConfig value={{ provider: () => new Map(), revalidateOnFocus: false, shouldRetryOnError: false }}>{node}</SWRConfig>
  );
  const view = render(wrap(modalElement({ ...props, onSubmit })));
  /** The server's refusal arrives after the modal is open, as after a submit. */
  const fail = (error: ApiError) => view.rerender(wrap(modalElement({ ...props, error, onSubmit })));
  return { fail, onSubmit };
}

/** Opens a Select by its label and clicks the option with that text. */
async function choose(user: ReturnType<typeof userEvent.setup>, label: string, option: string) {
  await user.click(screen.getByRole('combobox', { name: label }));
  // Another Select may show the same text as its value, so look inside the open dropdown.
  const dropdown = await waitFor(() => {
    const open = document.querySelector<HTMLElement>('.ant-select-dropdown:not(.ant-select-dropdown-hidden)');
    if (!open) throw new Error('no open dropdown');
    return open;
  });
  await user.click(await within(dropdown).findByTitle(option));
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
    expect(within(provider.closest('.ant-form-item') as HTMLElement).getByTitle('None')).toBeTruthy();
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
  it('submits the picked Basic auth group, and null for None', async () => {
    const user = userEvent.setup();
    const { onSubmit } = renderModal({ domain });
    await screen.findByText('Edit domain');
    expect(screen.getByText(AUTH_HELP)).toBeTruthy();

    await choose(user, 'Basic auth', 'Staging');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(onSubmit).toHaveBeenLastCalledWith(expect.objectContaining({ authGroupId: 4 }));

    await choose(user, 'Basic auth', 'None');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(2));
    expect(onSubmit).toHaveBeenLastCalledWith(expect.objectContaining({ authGroupId: null }));
  });

  it('starts a stored group selected', async () => {
    renderModal({ domain: { ...domain, authGroupId: 8, authGroupName: 'Partners' } });
    await screen.findByText('Edit domain');
    expect(await screen.findByText('Partners')).toBeTruthy();
  });

  it('offers + New group to an admin only', async () => {
    const user = userEvent.setup();
    renderModal();
    await user.click(await screen.findByRole('combobox', { name: 'Basic auth' }));
    expect(await screen.findByRole('button', { name: /new group/i })).toBeTruthy();
  });

  it('hides + New group from a non-admin', async () => {
    session.admin = false;
    const user = userEvent.setup();
    renderModal();
    await user.click(await screen.findByRole('combobox', { name: 'Basic auth' }));
    await screen.findByTitle('Staging');
    expect(screen.queryByRole('button', { name: /new group/i })).toBeNull();
  });

  it('creates a group from the footer and selects it', async () => {
    api.createAuthGroup.mockResolvedValue({ id: 12, name: 'Fresh' });
    const user = userEvent.setup();
    renderModal();
    await user.click(await screen.findByRole('combobox', { name: 'Basic auth' }));
    await user.click(await screen.findByRole('button', { name: /new group/i }));

    const dialog = (await screen.findAllByRole('dialog')).find((d) => within(d).queryByLabelText('Group name'));
    if (!dialog) throw new Error('no group dialog');
    await user.type(within(dialog).getByLabelText('Group name'), 'Fresh');
    await user.type(within(dialog).getByLabelText('Username'), 'alice');
    await user.type(within(dialog).getByLabelText('Password'), 'password-one');
    await user.click(within(dialog).getByRole('button', { name: 'Create' }));

    await waitFor(() =>
      expect(api.createAuthGroup).toHaveBeenCalledWith({
        name: 'Fresh',
        users: [{ password: 'password-one', username: 'alice' }],
      }),
    );
    // The new group is the Select's value now.
    await waitFor(() => {
      const item = screen.getByRole('combobox', { name: 'Basic auth' }).closest('.ant-form-item') as HTMLElement;
      expect(within(item).getByTitle('Fresh')).toBeTruthy();
    });
  });
});
