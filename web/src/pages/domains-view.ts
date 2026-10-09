import type { Compose, Domain, DomainInput } from '../data/types';

/**
 * View rules for the Domains tab, kept pure so the form's mapping to the API
 * payload, the redeploy banner and the HTTPS tag are testable without a modal
 * or a store.
 */

/** The Add/Edit form's values: the API payload plus the switch that is not sent. */
export type DomainFormValues = DomainInput & { customEntrypoint: boolean };

/** The form's starting values; an existing domain fills them from its stored fields. */
export function domainFormValues(domain?: Domain): DomainFormValues {
  if (!domain) {
    return {
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
    };
  }
  return {
    host: domain.host,
    path: domain.path,
    internalPath: domain.internalPath,
    stripPath: domain.stripPath,
    service: domain.service,
    port: domain.port,
    entrypoint: domain.entrypoint,
    https: domain.https,
    certProvider: domain.certProvider,
    certResolver: domain.certResolver,
    middlewares: domain.middlewares,
    customEntrypoint: domain.entrypoint !== '',
  };
}

/**
 * The payload for the API. A field whose switch is off is cleared rather than
 * sent with a stale value: a hidden entrypoint, a provider and resolver with
 * HTTPS off, and a resolver unless the provider is custom.
 */
export function domainInput(values: DomainFormValues): DomainInput {
  const https = values.https;
  const certProvider = https ? values.certProvider : 'none';
  return {
    host: values.host,
    path: values.path,
    internalPath: values.internalPath,
    stripPath: values.stripPath,
    service: values.service,
    port: values.port,
    entrypoint: values.customEntrypoint ? values.entrypoint : '',
    https,
    certProvider,
    certResolver: certProvider === 'custom' ? values.certResolver : '',
    middlewares: values.middlewares,
  };
}

/**
 * Whether the Domains tab shows the redeploy banner. A Compose that never
 * deployed has no banner, because its first deploy includes the domains. Both
 * timestamps have one-second resolution, so a tie counts as pending.
 */
export function domainsPendingRedeploy(compose: Pick<Compose, 'domainsChangedAt' | 'lastDeployedAt'>): boolean {
  if (!compose.lastDeployedAt || !compose.domainsChangedAt) {
    return false;
  }
  return Date.parse(compose.domainsChangedAt) >= Date.parse(compose.lastDeployedAt);
}

/** The HTTPS column's tag. */
export function httpsTag(domain: Domain): string {
  if (!domain.https) {
    return 'HTTP';
  }
  switch (domain.certProvider) {
    case 'letsencrypt':
      return "Let's Encrypt";
    case 'custom':
      return `custom: ${domain.certResolver}`;
    default:
      return 'TLS (default cert)';
  }
}

/** The link the host column opens in a new tab. */
export function domainUrl(domain: Domain): string {
  return `${domain.https ? 'https' : 'http'}://${domain.host}${domain.path}`;
}
