import type { ExternalRegistry, RegistryCheckStep, RegistryStatus, RegistryTag } from '../data/types';

/** Under the token field: a leaked pull-only token can pull, not push (spec, Decisions). */
export const TOKEN_HINT = 'Use a token that can only pull, such as a GitHub PAT with read:packages.';

export type TestState = 'untested' | 'passed' | 'failed';

/** What the last Test said about an External registry's login. */
export function testState(registry: ExternalRegistry): TestState {
  if (registry.lastTestAt === null) return 'untested';
  return registry.lastTestOk ? 'passed' : 'failed';
}

/** `docker login` for a new Push credential; the token is typed at the prompt. */
export function loginCommand(hostname: string, name: string): string {
  return `docker login ${hostname} -u ${name}`;
}

/** A GitHub Actions step logging in with a Push credential stored as a secret. */
export function loginActionSnippet(hostname: string, name: string): string {
  return [
    '- uses: docker/login-action@v3',
    '  with:',
    `    registry: ${hostname}`,
    `    username: ${name}`,
    '    password: ${{ secrets.RELIST_REGISTRY_TOKEN }}',
  ].join('\n');
}

/** Deleting is by digest, so every tag on the same manifest goes too; the asked tag first. */
export function sharedTags(tags: RegistryTag[], tag: string): string[] {
  const digest = tags.find((t) => t.tag === tag)?.digest;
  if (digest === undefined) return [tag];
  return [tag, ...tags.filter((t) => t.digest === digest && t.tag !== tag).map((t) => t.tag)];
}

export function shortDigest(digest: string): string {
  return digest.replace(/^sha256:/, '').slice(0, 12);
}

export const REGISTRY_STATUS: Record<RegistryStatus, { color: string; label: string }> = {
  drift: { color: 'warning', label: 'Drift' },
  error: { color: 'error', label: 'Error' },
  off: { color: 'default', label: 'Off' },
  'read-only': { color: 'processing', label: 'Read-only (cleanup)' },
  running: { color: 'success', label: 'Running' },
  stopped: { color: 'warning', label: 'Stopped' },
};

/** Where Check endpoint's request to https://<hostname>/v2/ failed. */
export const CHECK_STEP: Record<RegistryCheckStep, string> = {
  auth: 'Login',
  connect: 'Connection',
  dns: 'DNS',
  http: 'Registry',
  tls: 'TLS',
};
