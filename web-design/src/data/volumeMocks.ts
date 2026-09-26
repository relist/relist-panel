import type { Volume, VolumeEntry, VolumeExistence } from './types';

/**
 * Volume fixtures for the redesign mock.
 *
 * Kept beside `mocks.ts` rather than inside it: the Monacos, log fixtures and
 * metric generators in there are unrelated to this feature, and the trees below
 * are long enough that mixing them in would bury both.
 *
 * The fixtures are shaped to make every state this feature can be in reachable
 * by clicking, which is the point of a design pass:
 *
 *  - Compose 1 (`web`, running) has all six Volume shapes — a named volume, a
 *    second named volume, a bind under the data directory whose existence is
 *    known, a bind outside it whose existence is not, a read-only bind, and a
 *    bind whose source was checked and is not there (ticket 04: the absent
 *    state the Start Handle dialog refuses and the Volumes tab disables Open
 *    for had no fixture before).
 *  - Its uploads Volume carries the whole tree: nested directories, an empty
 *    directory, a file too large for the editor, a symlink that stays inside,
 *    a symlink that escapes, and a filename with a newline in it.
 *  - Compose 3 (`metrics`) has never been deployed, so its Volumes cannot be
 *    resolved at all — the tab's unavailable state.
 *  - Compose 4 (`staging` web) resolves to an empty list, for the empty state.
 *  - Its uploads Volume is nearly full (512 KiB free, ticket 02): the
 *    upload-no-space refusal is a fact about the Volume, not a painted flag.
 */

const DATA_DIR = '/var/lib/relist-panel';

const mtime = (minutesAgo: number) => new Date(Date.now() - minutesAgo * 60_000).toISOString();

/** Files in the demo tree are owned by the app's uid, which is what the dialog defaults to. */
const OWNER = { gid: 999, uid: 999 } as const;

function file(name: string, size: number, minutesAgo: number): VolumeEntry {
  return { ...OWNER, kind: 'file', mode: '0644', modifiedAt: mtime(minutesAgo), name, size };
}

function dir(name: string, minutesAgo: number): VolumeEntry {
  return { ...OWNER, kind: 'dir', mode: '0755', modifiedAt: mtime(minutesAgo), name, size: 4096 };
}

function symlink(name: string, linkTarget: string, linkEscapes: boolean, minutesAgo: number): VolumeEntry {
  return { ...OWNER, kind: 'symlink', linkEscapes, linkTarget, mode: '0777', modifiedAt: mtime(minutesAgo), name, size: linkTarget.length };
}

function bind(declaredAs: string, target: string, services: string[], readOnly: boolean, existence: VolumeExistence): Omit<Volume, 'composeId' | 'id'> {
  // A relative bind resolves against the Compose file's own directory, which is
  // why the resolved source sits under the data directory at all (ADR-0002).
  const source = declaredAs.startsWith('.')
    ? `${DATA_DIR}/my-saas/production/web/${declaredAs.replace(/^\.\//, '')}`
    : declaredAs;
  return { declaredAs, existence, kind: 'bind', readOnly, services, source, target };
}

function named(declaredAs: string, realName: string, target: string, services: string[], readOnly = false): Omit<Volume, 'composeId' | 'id'> {
  return { declaredAs, existence: 'present', kind: 'volume', readOnly, services, source: realName, target };
}

const composeOne: Omit<Volume, 'composeId' | 'id'>[] = [
  named('pgdata', 'my-saas_production_web_pgdata', '/var/lib/postgresql/data', ['api']),
  named('redisdata', 'my-saas_production_web_redisdata', '/data', ['redis']),
  bind('./uploads', '/app/uploads', ['api'], false, 'present'),
  bind('./config', '/app/config', ['api'], true, 'present'),
  bind('/srv/legacy-images', '/app/legacy', ['api'], true, 'unverifiable'),
  // Declared in the YAML but never provisioned: the source was stat-ed and is
  // not there, so the panel refuses to open it — mounting it would create it.
  bind('./retired', '/app/retired', ['api'], false, 'absent'),
];

export const volumeFixtures: Record<number, Omit<Volume, 'composeId' | 'id'>[]> = {
  1: composeOne,
  2: [named('workerstate', 'my-saas_production_worker_state', '/var/lib/worker', ['worker'])],
  5: [named('sitedata', 'landing_production_site_data', '/usr/share/nginx/html', ['site'])],
  6: [
    named('uptimekuma', 'internal-tools_production_uptime_kuma', '/app/data', ['uptime']),
    bind('./backups', '/backups', ['uptime'], false, 'present'),
  ],
  7: [named('registrydata', 'internal-tools_production_registry_registry', '/var/lib/registry', ['registry'])],
  8: [named('ghostcontent', 'blog_production_ghost_content', '/var/lib/ghost/content', ['ghost'])],
  // Compose 4 (`staging` web) deliberately carries no Volumes: the empty state.
  4: [],
};

/**
 * Whether a Compose's Volumes could be resolved at all. Resolution goes through
 * `docker compose config`, which fails with `couldn't find env file` until the
 * Compose has been deployed once — the panel always passes `--env-file`.
 */
export const volumeResolution: Record<number, { ok: true } | { ok: false; error: string }> = {
  3: { error: `couldn't find env file: ${DATA_DIR}/my-saas/production/metrics/.env`, ok: false },
};

export function resolveVolumes(composeId: number): { ok: true; volumes: Volume[] } | { ok: false; error: string } {
  const resolution = volumeResolution[composeId];
  if (resolution && !resolution.ok) {
    return resolution;
  }
  return {
    ok: true,
    volumes: (volumeFixtures[composeId] ?? []).map((volume) => ({
      ...volume,
      composeId,
      id: `${composeId}:${volume.target}`,
    })),
  };
}

/** The demo tree, on Compose 1's uploads bind. */
export const volumeTree: Record<string, Record<string, VolumeEntry[]>> = {
  '1:/app/uploads': {
    '/': [
      dir('avatars', 1_440),
      dir('incoming', 12),
      dir('invoices', 180),
      file('README.txt', 1_204, 4_320),
      // 4.2 MB: past the 1 MB editor cap, so it opens the refusal instead.
      file('export.csv', 4_404_019, 300),
      // A real filename — the API returns it from ReadDir, and nothing parses it
      // out of a listing, so the newline survives.
      file('odd\nname.txt', 96, 60),
      symlink('latest', 'invoices/2026-09', false, 2_880),
      // Points outside the Volume. Shown with its target; opening it is refused.
      symlink('legacy', '/srv/legacy-images', true, 5_760),
    ],
    '/avatars': [
      file('3f9a1c.webp', 18_240, 1_440),
      file('8c02de.webp', 22_110, 2_100),
      file('c41b77.webp', 9_880, 90),
      file('fee010.webp', 24_900, 6_000),
    ],
    '/incoming': [],
    '/invoices': [dir('2026-07', 8_640), dir('2026-08', 4_320), dir('2026-09', 180), dir('2026-10', 12)],
    '/invoices/2026-07': [file('acme-0702.pdf', 164_010, 8_640), file('globex-0719.pdf', 201_440, 8_000)],
    '/invoices/2026-08': [file('acme-0805.pdf', 176_220, 4_320), file('initech-0821.pdf', 88_400, 3_900)],
    '/invoices/2026-09': [
      file('acme-0902.pdf', 181_990, 1_500),
      file('acme-0914.pdf', 184_320, 600),
      file('globex-0928.pdf', 209_770, 180),
    ],
    // Emptied by the last run — the Explorer's empty-directory state.
    '/invoices/2026-10': [],
  },
};

/**
 * One directory of a Volume. A Volume with no fixture tree is a fresh named
 * volume, which really is empty — saying so is more honest than inventing
 * contents, and it is also the empty-directory state.
 *
 * `path` is relative to the Volume root and is `''` at the root, matching the
 * helper protocol, where paths are always relative to `/data`. The fixtures key
 * the root as `'/'` instead, hence the small translation.
 */
export function listVolumeDir(volumeId: string, path: string): VolumeEntry[] {
  const tree = volumeTree[volumeId];
  if (!tree) return [];
  return tree[normalizeDirPath(path)] ?? [];
}

/**
 * One spelling for a directory path, used by everything here.
 *
 * The protocol says paths are relative to the Volume root and `''` at the root;
 * the fixtures above key the root as `'/'` and subdirectories with a leading
 * slash. Normalizing in one place is what keeps those two conventions from
 * meeting halfway and silently listing an empty directory — which is exactly
 * what they did the first time.
 */
function normalizeDirPath(path: string): string {
  const trimmed = path.replace(/^\/+|\/+$/g, '');
  return trimmed === '' ? '/' : `/${trimmed}`;
}

/**
 * Directories whose mode denies the handle's uid, so opening one fails the way
 * it would on a real Volume. Worth having in the mock: it is the state an
 * operator is most likely to hit and least likely to understand, and the message
 * has to name the uid the handle runs as, because that is the thing they can
 * change.
 */
export const unreadablePaths: Record<string, string[]> = {
  '1:/app/uploads': ['/avatars'],
};

export function unreadableReason(volumeId: string, path: string): string | undefined {
  if (!unreadablePaths[volumeId]?.includes(normalizeDirPath(path))) return undefined;
  return 'openat: permission denied — the Volume Handle runs as uid 999 and this directory is 0700 root:root';
}

/**
 * Declared free space, for the upload-refusal scene (`seed:volume.uploadNoSpace`,
 * ticket 02): uploads is nearly full, so a file well under the upload cap still
 * does not fit on the Volume. Mirrors `unreadablePaths` — the refusal carries
 * the helper's own words and is a fact about the Volume, consulted where the
 * incoming size is known (the Explorer's `upload`).
 */
export const volumeFreeBytes: Record<string, number> = {
  '1:/app/uploads': 524_288,
};

/** Why a write of `incomingBytes` would be refused, the way the helper would say it. */
export function volumeWriteRefusal(volumeId: string, incomingBytes: number): string | undefined {
  const free = volumeFreeBytes[volumeId];
  if (free === undefined || incomingBytes <= free) return undefined;
  return `write: no space left on device — ${incomingBytes} bytes does not fit in ${free} bytes free on ${volumeId}`;
}

/** A file above the editor's cap. Opening it refuses rather than hanging Monaco. */
export const EDITOR_MAX_BYTES = 1_048_576;
export const UPLOAD_MAX_BYTES = 1_073_741_824;

/**
 * What an archive of a directory would contain.
 *
 * Counted by walking the tree the panel would walk anyway, which is the whole
 * reason the disclosure below is affordable: by the time the operator asks for
 * the archive, the symlink count is already known, so the hazard of extracting
 * it somewhere else can be stated up front instead of being discovered later.
 */
export type ArchiveSummary = {
  files: number;
  bytes: number;
  symlinks: number;
  /** Symlinks whose target resolves outside the Volume. Stored as-is, never followed. */
  escapingSymlinks: number;
};

export function summarizeArchive(volumeId: string, path: string): ArchiveSummary {
  const summary: ArchiveSummary = { bytes: 0, escapingSymlinks: 0, files: 0, symlinks: 0 };
  const walk = (dirPath: string) => {
    for (const entry of listVolumeDir(volumeId, dirPath)) {
      if (entry.kind === 'dir') {
        walk(dirPath ? `${dirPath}/${entry.name}` : entry.name);
        continue;
      }
      summary.files += 1;
      summary.bytes += entry.size;
      if (entry.kind === 'symlink') {
        summary.symlinks += 1;
        if (entry.linkEscapes) {
          summary.escapingSymlinks += 1;
        }
      }
    }
  };
  walk(path);
  return summary;
}

/** Idle auto-stop, matching `RELIST_VOLUME_IDLE_TIMEOUT`'s default. */
export const IDLE_TIMEOUT_MINUTES = 30;

const README = `Uploads for the web Service.

Written by the app at uid 999. The invoices/ tree is append-only —
the cleanup job empties each month into cold storage on the 1st, which
is why invoices/2026-10 is empty right now.

There is a symlink called legacy pointing at /srv/legacy-images, which
lives outside this Volume. The Explorer shows it and refuses to follow
it. That is the Volume telling the truth, not a bug.
`;

const NGINX_CONF = `server {
    listen 80;
    server_name _;

    root /app/uploads;
    client_max_body_size 25m;

    location /invoices/ {
        # Invoices are served straight off the Volume, so a file dropped
        # here is live as soon as the write returns.
        add_header Cache-Control "private, max-age=300";
    }
}
`;

/**
 * The editor's contents for a path. Only the two files the design pass needs to
 * demonstrate an edit are given real text; everything else gets a short stand-in
 * so the editor is never blank for no reason.
 */
export function fixtureFileContents(path: string): string {
  if (path.endsWith('README.txt')) return README;
  if (path.endsWith('nginx.conf')) return NGINX_CONF;
  const name = path.split('/').filter(Boolean).pop() ?? 'file';
  return `${name}\n\n(Design mock: this file has no fixture contents.)\n`;
}
