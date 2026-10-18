/**
 * A server Migration's wire types (ADR-0018). The old host reports its lock as
 * `MigrationStatus`; the new host's setup screen polls `MigrationProgress`.
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

export type MigrationPhase = '' | 'connected' | 'locking' | 'pulling' | 'paused' | 'finalizing' | 'done';

export type MigrationItemKind = 'database' | 'data-dir' | 'volume' | 'host-path' | 'image';

export type MigrationItem = {
  id: string;
  kind: MigrationItemKind;
  size: number;
  volume?: { name: string };
  hostPath?: string;
  image?: { id: string; tags: string[]; repoDigests: string[]; size: number };
};

export type HostPathCandidate = { path: string; system: boolean; size: number; composes: string[] };

export type SourceInfo = {
  protocol: number;
  schemaVersion: number;
  dataDir: string;
  hostPaths: HostPathCandidate[];
  volumes: MigrationItem[];
  images: MigrationItem[];
  dataDirSize: number;
  databaseSize: number;
  activeDeployment?: string;
  warnings: string[];
};

export type MigrationCheck = { blocking: boolean; message: string };

export type MigrationItemProgress = {
  id: string;
  kind: MigrationItemKind;
  size: number;
  done: number;
  state: 'pending' | 'running' | 'done' | 'failed';
  attempts: number;
  error?: string;
};

export type MigrationSummary = {
  deployments: { composeId: number; number: number; error?: string }[];
  warnings: string[];
  dns: string[];
};

export type MigrationProgress = {
  phase: MigrationPhase;
  sourceUrl: string;
  lockStep: MigrationStep;
  selected: string[];
  items: MigrationItemProgress[];
  checks: MigrationCheck[];
  info: SourceInfo | null;
  warnings: string[];
  error?: string;
  summary?: MigrationSummary;
};
