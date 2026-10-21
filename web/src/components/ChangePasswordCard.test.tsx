// @vitest-environment jsdom
import '../test/dom-setup';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { App } from 'antd';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { ChangePasswordCard } from './ChangePasswordCard';

const mocks = vi.hoisted(() => ({ changePassword: vi.fn() }));
vi.mock('../data/api', () => ({ api: mocks }));

afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});

function show(props: { passwordFromEnv?: boolean; twoFactorEnabled?: boolean } = {}) {
  const onChanged = vi.fn();
  render(
    <App>
      <ChangePasswordCard onChanged={onChanged} passwordFromEnv={!!props.passwordFromEnv} twoFactorEnabled={!!props.twoFactorEnabled} />
    </App>,
  );
  return onChanged;
}

function fill(dialog: HTMLElement, label: string, value: string) {
  fireEvent.change(within(dialog).getByLabelText(label), { target: { value } });
}

describe('ChangePasswordCard', () => {
  it('warns while the password is still the one from the env', () => {
    show({ passwordFromEnv: true });
    expect(screen.getByText(/still the one in RELIST_ADMIN_PASSWORD/)).toBeTruthy();
  });

  it('keeps the button default: primary is for Deploy, Create and Save (GUIDELINES §2)', () => {
    show({ passwordFromEnv: true });
    expect(screen.getByRole('button', { name: 'Change password' }).className).not.toContain('ant-btn-primary');
  });

  it('changes the password with the current one', async () => {
    mocks.changePassword.mockResolvedValue(undefined);
    const onChanged = show();
    fireEvent.click(screen.getByRole('button', { name: 'Change password' }));
    const dialog = await screen.findByRole('dialog');
    fill(dialog, 'Current password', 'correct horse battery staple');
    fill(dialog, 'New password', 'a brand new password');
    fill(dialog, 'Confirm new password', 'a brand new password');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Change password' }));
    await waitFor(() =>
      expect(mocks.changePassword).toHaveBeenCalledWith('correct horse battery staple', 'a brand new password', undefined),
    );
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
  });

  it('refuses a confirmation that does not match', async () => {
    show();
    fireEvent.click(screen.getByRole('button', { name: 'Change password' }));
    const dialog = await screen.findByRole('dialog');
    fill(dialog, 'Current password', 'correct horse battery staple');
    fill(dialog, 'New password', 'a brand new password');
    fill(dialog, 'Confirm new password', 'something else entirely');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Change password' }));
    expect(await within(dialog).findByText('The two passwords differ')).toBeTruthy();
    expect(mocks.changePassword).not.toHaveBeenCalled();
  });

  it('asks for a code when two-factor is on', async () => {
    mocks.changePassword.mockResolvedValue(undefined);
    show({ twoFactorEnabled: true });
    fireEvent.click(screen.getByRole('button', { name: 'Change password' }));
    const dialog = await screen.findByRole('dialog');
    fill(dialog, 'Current password', 'correct horse battery staple');
    fill(dialog, 'New password', 'a brand new password');
    fill(dialog, 'Confirm new password', 'a brand new password');
    fill(dialog, 'Authenticator code', '123456');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Change password' }));
    await waitFor(() =>
      expect(mocks.changePassword).toHaveBeenCalledWith('correct horse battery staple', 'a brand new password', '123456'),
    );
  });
});
