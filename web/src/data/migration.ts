/**
 * A server Migration's wire types (ADR-0018). The old host reports its lock as
 * `MigrationStatus`; the pull itself is driven from the terminal (ADR-0019).
 */

export type MigrationStep = '' | 'locked' | 'stopping' | 'maintenance' | 'ready' | 'migrated';

export type TokenLifetime = '1h' | '24h' | '168h';

export type MigrationStatus = {
  step: MigrationStep;
  claimedBy: string;
  targetUrl: string;
  lockedAt: string | null;
  migratedAt: string | null;
  tokenExpiresAt: string | null;
};

export type MigrationToken = { token: string; expiresAt: string };
