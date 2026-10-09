import { describe, expect, it } from 'vitest';

import type { Compose, Domain } from '../data/types';
import { domainFormValues, domainInput, domainUrl, domainsPendingRedeploy, httpsTag } from './domains-view';

const domain: Domain = {
  id: 3,
  host: 'api.example.com',
  path: '/v1',
  internalPath: '/',
  stripPath: true,
  service: 'web',
  port: 3000,
  entrypoint: 'custom-ep',
  https: true,
  certProvider: 'custom',
  certResolver: 'dns',
  middlewares: ['rate-limit@file'],
  createdAt: '2026-10-01T00:00:00Z',
  updatedAt: '2026-10-01T00:00:00Z',
};

const values = (overrides: Partial<ReturnType<typeof domainFormValues>>) => ({
  ...domainFormValues(undefined),
  host: 'api.example.com',
  service: 'web',
  port: 3000,
  ...overrides,
});

describe('domainInput', () => {
  it('clears entrypoint when the custom entrypoint switch is off', () => {
    expect(domainInput(values({ customEntrypoint: false, entrypoint: 'stale' })).entrypoint).toBe('');
    expect(domainInput(values({ customEntrypoint: true, entrypoint: 'web-alt' })).entrypoint).toBe('web-alt');
  });

  it('sends no provider and no resolver when https is off', () => {
    const input = domainInput(values({ https: false, certProvider: 'custom', certResolver: 'dns' }));
    expect(input.certProvider).toBe('none');
    expect(input.certResolver).toBe('');
  });

  it('clears the resolver when the provider is not custom', () => {
    expect(domainInput(values({ https: true, certProvider: 'letsencrypt', certResolver: 'stale' })).certResolver).toBe('');
    expect(domainInput(values({ https: true, certProvider: 'none', certResolver: 'stale' })).certResolver).toBe('');
  });

  it('keeps the resolver for the custom provider', () => {
    expect(domainInput(values({ https: true, certProvider: 'custom', certResolver: 'dns' })).certResolver).toBe('dns');
  });
});

describe('domainFormValues', () => {
  it('defaults an http domain to lets encrypt so switching https on is sensible', () => {
    const http: Domain = { ...domain, https: false, certProvider: 'none', certResolver: '' };
    const form = domainFormValues(http);
    expect(form.https).toBe(false);
    expect(form.certProvider).toBe('letsencrypt');
    expect(form.certResolver).toBe('');
  });

  it('gives the defaults for a new domain', () => {
    expect(domainFormValues(undefined)).toEqual({
      host: '',
      path: '/',
      internalPath: '/',
      stripPath: false,
      service: '',
      port: 80,
      entrypoint: '',
      https: false,
      certProvider: 'letsencrypt',
      certResolver: '',
      middlewares: [],
      customEntrypoint: false,
    });
  });

  it('copies a stored domain and turns the custom entrypoint on when it has one', () => {
    expect(domainFormValues(domain)).toEqual({
      host: 'api.example.com',
      path: '/v1',
      internalPath: '/',
      stripPath: true,
      service: 'web',
      port: 3000,
      entrypoint: 'custom-ep',
      https: true,
      certProvider: 'custom',
      certResolver: 'dns',
      middlewares: ['rate-limit@file'],
      customEntrypoint: true,
    });
    expect(domainFormValues({ ...domain, entrypoint: '' }).customEntrypoint).toBe(false);
  });
});

describe('domainsPendingRedeploy', () => {
  const compose = (domainsChangedAt: string | null | undefined, lastDeployedAt: string | null | undefined) =>
    ({ domainsChangedAt, lastDeployedAt }) as Pick<Compose, 'domainsChangedAt' | 'lastDeployedAt'>;

  it('is false when the Compose has never deployed', () => {
    expect(domainsPendingRedeploy(compose('2026-10-02T00:00:00Z', null))).toBe(false);
    expect(domainsPendingRedeploy(compose('2026-10-02T00:00:00Z', undefined))).toBe(false);
  });

  it('is false when no domain has ever changed', () => {
    expect(domainsPendingRedeploy(compose(null, '2026-10-01T00:00:00Z'))).toBe(false);
    expect(domainsPendingRedeploy(compose(undefined, '2026-10-01T00:00:00Z'))).toBe(false);
  });

  it('is true when the change lands on the same second as the deploy', () => {
    expect(domainsPendingRedeploy(compose('2026-10-01T00:00:00Z', '2026-10-01T00:00:00Z'))).toBe(true);
  });

  it('is true when the change is after the deploy', () => {
    expect(domainsPendingRedeploy(compose('2026-10-02T00:00:00Z', '2026-10-01T00:00:00Z'))).toBe(true);
  });

  it('is false when the change is before the deploy', () => {
    expect(domainsPendingRedeploy(compose('2026-10-01T00:00:00Z', '2026-10-02T00:00:00Z'))).toBe(false);
  });
});

describe('httpsTag', () => {
  it('gives the four tag strings', () => {
    expect(httpsTag({ ...domain, https: false, certProvider: 'none', certResolver: '' })).toBe('HTTP');
    expect(httpsTag({ ...domain, https: true, certProvider: 'letsencrypt', certResolver: '' })).toBe("Let's Encrypt");
    expect(httpsTag({ ...domain, https: true, certProvider: 'custom', certResolver: 'dns' })).toBe('custom: dns');
    expect(httpsTag({ ...domain, https: true, certProvider: 'none', certResolver: '' })).toBe('TLS (default cert)');
  });
});

describe('domainUrl', () => {
  it('uses https for an HTTPS domain and http otherwise', () => {
    expect(domainUrl(domain)).toBe('https://api.example.com/v1');
    expect(domainUrl({ ...domain, https: false, certProvider: 'none', path: '/' })).toBe('http://api.example.com/');
  });
});
