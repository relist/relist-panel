// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../data/api';
import type { Certificate } from '../data/types';
import { CertificatesPage } from './CertificatesPage';

const api = vi.hoisted(() => ({
  createCertificate: vi.fn(),
  deleteCertificate: vi.fn(),
  listCertificates: vi.fn(),
  replaceCertificate: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

const CERT = '-----BEGIN CERTIFICATE-----\nAAA\n-----END CERTIFICATE-----';
const KEY = '-----BEGIN PRIVATE KEY-----\nBBB\n-----END PRIVATE KEY-----';

const shop: Certificate = {
  createdAt: '2026-01-02T03:04:05Z',
  id: 1,
  issuer: 'Sectigo RSA',
  name: 'shop',
  notAfter: '2099-01-01T00:00:00Z',
  sans: ['shop.example.com', '*.shop.example.com'],
  updatedAt: '2026-01-02T03:04:05Z',
};
const old: Certificate = { ...shop, id: 2, name: 'old', notAfter: '2020-01-01T00:00:00Z', sans: ['old.example.com'] };

function show() {
  return render(
    <SWRConfig value={{ provider: () => new Map(), revalidateOnFocus: false, shouldRetryOnError: false }}>
      <MemoryRouter>
        <CertificatesPage />
      </MemoryRouter>
    </SWRConfig>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
});

describe('CertificatesPage', () => {
  it('lists name, SANs, issuer and an Expired tag', async () => {
    api.listCertificates.mockResolvedValue([shop, old]);
    show();

    expect(await screen.findByText('shop')).toBeTruthy();
    expect(screen.getByText('shop.example.com')).toBeTruthy();
    expect(screen.getByText('*.shop.example.com')).toBeTruthy();
    expect(screen.getAllByText('Sectigo RSA')).toHaveLength(2);
    expect(screen.getByText('Expired')).toBeTruthy();
    expect(
      screen.getByText(
        'Traefik serves an uploaded certificate to any HTTPS domain set to provider None whose host it covers. Changes apply immediately.',
      ),
    ).toBeTruthy();
  });

  it('adds a certificate', async () => {
    api.listCertificates.mockResolvedValue([]);
    api.createCertificate.mockResolvedValue(shop);
    const user = userEvent.setup();
    show();
    await screen.findByText('No certificates yet');

    await user.click(screen.getAllByRole('button', { name: /add certificate/i })[0]);
    await user.type(await screen.findByLabelText('Certificate Name'), 'shop');
    await user.click(screen.getByLabelText('Certificate Data'));
    await user.paste(CERT);
    await user.click(screen.getByLabelText('Private Key'));
    await user.paste(KEY);
    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() =>
      expect(api.createCertificate).toHaveBeenCalledWith({ certificate: CERT, name: 'shop', privateKey: KEY }),
    );
  });

  it('shows a field error under that field', async () => {
    api.listCertificates.mockResolvedValue([]);
    api.createCertificate.mockRejectedValue(
      new ApiError(400, 'private key does not match the certificate', 'privateKey'),
    );
    const user = userEvent.setup();
    show();
    await screen.findByText('No certificates yet');

    await user.click(screen.getAllByRole('button', { name: /add certificate/i })[0]);
    await user.type(await screen.findByLabelText('Certificate Name'), 'shop');
    await user.click(screen.getByLabelText('Certificate Data'));
    await user.paste(CERT);
    await user.click(screen.getByLabelText('Private Key'));
    await user.paste(KEY);
    await user.click(screen.getByRole('button', { name: 'Create' }));

    expect(await screen.findByText('private key does not match the certificate')).toBeTruthy();
    expect(screen.getByRole('dialog')).toBeTruthy();
  });

  it('replaces a certificate without sending its name', async () => {
    api.listCertificates.mockResolvedValue([shop]);
    api.replaceCertificate.mockResolvedValue(shop);
    const user = userEvent.setup();
    show();
    await screen.findByText('shop');

    await user.click(screen.getByRole('button', { name: 'Replace shop' }));
    const dialog = await screen.findByRole('dialog');
    expect((within(dialog).getByLabelText('Certificate Name') as HTMLInputElement).disabled).toBe(true);
    await user.click(within(dialog).getByLabelText('Certificate Data'));
    await user.paste(CERT);
    await user.click(within(dialog).getByLabelText('Private Key'));
    await user.paste(KEY);
    await user.click(within(dialog).getByRole('button', { name: 'Replace' }));

    await waitFor(() => expect(api.replaceCertificate).toHaveBeenCalledWith(1, { certificate: CERT, privateKey: KEY }));
  });

  it('deletes after a Popconfirm', async () => {
    api.listCertificates.mockResolvedValue([shop]);
    api.deleteCertificate.mockResolvedValue(undefined);
    const user = userEvent.setup();
    show();
    await screen.findByText('shop');

    await user.click(screen.getByRole('button', { name: 'Delete shop' }));
    expect(await screen.findByText('Delete certificate shop?')).toBeTruthy();
    expect(screen.getByText("Domains it covers fall back to Traefik's default certificate.")).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(api.deleteCertificate).toHaveBeenCalledWith(1));
  });
});
