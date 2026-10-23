// @vitest-environment jsdom
import '../test/dom-setup';
import { cleanup, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { PasswordFromEnvBanner } from './PasswordFromEnvBanner';

const mocks = vi.hoisted(() => ({ account: vi.fn() }));
vi.mock('../data/api', () => ({ api: mocks }));

afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});

function show() {
  return render(
    <MemoryRouter>
      <PasswordFromEnvBanner />
    </MemoryRouter>,
  );
}

describe('PasswordFromEnvBanner', () => {
  it('links to Account while the password is the one from the env', async () => {
    mocks.account.mockResolvedValue({ email: 'a@example.com', passwordFromEnv: true, twoFactorEnabled: false });
    show();
    expect((await screen.findByRole('link', { name: 'Change it in Account' })).getAttribute('href')).toBe('/account');
  });

  it('says nothing once the password was changed', async () => {
    mocks.account.mockResolvedValue({ email: 'a@example.com', passwordFromEnv: false, twoFactorEnabled: false });
    const { container } = show();
    await Promise.resolve();
    expect(container.textContent).toBe('');
  });
});
