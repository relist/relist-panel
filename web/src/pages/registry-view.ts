import type { ExternalRegistry } from '../data/types';

/** Under the token field: a leaked pull-only token can pull, not push (spec, Decisions). */
export const TOKEN_HINT = 'Use a token that can only pull, such as a GitHub PAT with read:packages.';

export type TestState = 'untested' | 'passed' | 'failed';

/** What the last Test said about an External registry's login. */
export function testState(registry: ExternalRegistry): TestState {
  if (registry.lastTestAt === null) return 'untested';
  return registry.lastTestOk ? 'passed' : 'failed';
}
