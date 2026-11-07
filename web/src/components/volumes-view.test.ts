import { describe, expect, it } from 'vitest';

import type { ComposeVolume, ComposeVolumes } from '../data/types';
import {
  cliMessage,
  downloadRefusal,
  isNotDeployed,
  orphansView,
  resolvedVolumePath,
  serviceUserRefusal,
  volumeLabel,
  volumeWarnings,
  volumesView,
} from './volumes-view';

function volume(overrides: Partial<ComposeVolume> = {}): ComposeVolume {
  return {
    kind: 'volume',
    logicalName: 'pgdata',
    source: 'demo_pgdata',
    uses: [{ service: 'api', target: '/var/lib/postgresql/data', readOnly: false }],
    preflight: { status: 'ok', warning: '' },
    ...overrides,
  };
}

function payload(overrides: Partial<ComposeVolumes> = {}): ComposeVolumes {
  return { available: true, deployed: true, services: [], volumes: [], ...overrides };
}

describe('volumesView', () => {
  it('is loading while the page has neither a payload nor an error', () => {
    expect(volumesView(undefined, undefined)).toEqual({ status: 'loading' });
  });

  it('is not-deployed, with no CLI message, when the API says no deploy has written the env file', () => {
    expect(volumesView(payload({ deployed: false }), undefined)).toEqual({ status: 'not-deployed' });
  });

  it('is hidden before it is not-deployed when the feature cannot run', () => {
    expect(volumesView(payload({ available: false, deployed: false, reason: 'no socket' }), undefined)).toEqual({
      status: 'hidden',
      reason: 'no socket',
    });
  });

  it('carries the CLI message in the not-deployed state, without the panel wrapper', () => {
    const error =
      "volume resolution failed: couldn't find env file: /var/lib/relist-panel/my-saas/production/metrics/.env";
    const view = volumesView(undefined, error);
    expect(view.status).toBe('not-deployed');
    if (view.status === 'not-deployed') {
      expect(view.message).toBe("couldn't find env file: /var/lib/relist-panel/my-saas/production/metrics/.env");
      expect(view.message).toContain("couldn't find env file");
    }
  });

  it('is hidden with the API reason when the feature cannot run', () => {
    const view = volumesView(
      payload({ available: false, reason: 'the panel must run in a container to reach the Docker socket' }),
      undefined,
    );
    expect(view).toEqual({
      status: 'hidden',
      reason: 'the panel must run in a container to reach the Docker socket',
    });
  });

  it('keeps any other failure as an error carrying the API words', () => {
    const view = volumesView(undefined, 'volume pre-flight failed: check named volume "demo_pgdata": docker: down');
    expect(view).toEqual({
      status: 'error',
      message: 'volume pre-flight failed: check named volume "demo_pgdata": docker: down',
    });
  });

  it('is empty when the Compose mounts nothing', () => {
    expect(volumesView(payload(), undefined)).toEqual({ status: 'empty' });
  });

  it('is ready with the volumes and their warnings otherwise', () => {
    const volumes = [volume(), volume({ kind: 'bind', logicalName: '', source: '/srv/legacy-images' })];
    const view = volumesView(payload({ volumes }), undefined);
    expect(view).toEqual({ status: 'ready', volumes, warnings: [] });
  });
});

describe('volumeWarnings', () => {
  it('keeps only the pre-flight outcomes that are not ok, in Volume order', () => {
    const missing = volume({
      kind: 'bind',
      logicalName: '',
      source: '/srv/retired',
      preflight: { status: 'missing', warning: 'bind source "/srv/retired" does not exist' },
    });
    const unverifiable = volume({
      kind: 'bind',
      logicalName: '',
      source: '/srv/legacy-images',
      preflight: {
        status: 'unverifiable',
        warning: 'cannot check "/srv/legacy-images": continuing will create it as root:root 0755',
      },
    });
    const warnings = volumeWarnings([volume(), unverifiable, missing]);
    expect(warnings.map((warning) => warning.status)).toEqual(['unverifiable', 'missing']);
    expect(warnings[0].volume).toBe(unverifiable);
    expect(warnings[0].message).toContain('root:root 0755');
    expect(warnings[1].message).toContain('does not exist');
  });
});

describe('resolvedVolumePath', () => {
  it('reads a named volume through its real name', () => {
    expect(resolvedVolumePath(volume())).toBe('/var/lib/docker/volumes/demo_pgdata/_data');
  });

  it('shows a bind at its own resolved source', () => {
    expect(resolvedVolumePath(volume({ kind: 'bind', logicalName: '', source: '/srv/legacy-images' }))).toBe(
      '/srv/legacy-images',
    );
  });
});

describe('volumeLabel', () => {
  it('leads with the logical name when the Compose declared one', () => {
    expect(volumeLabel(volume())).toBe('pgdata');
  });

  it('falls back to the resolved source for a bind', () => {
    expect(volumeLabel(volume({ kind: 'bind', logicalName: '', source: '/srv/legacy-images' }))).toBe(
      '/srv/legacy-images',
    );
  });
});

describe('downloadRefusal', () => {
  it('refuses a missing source with the API’s warning, because the mount would create it', () => {
    const missing = volume({ preflight: { status: 'missing', warning: 'bind source "/srv/retired" does not exist' } });
    expect(downloadRefusal(missing)).toBe('bind source "/srv/retired" does not exist');
  });

  it('allows an ok or unverifiable source — unverifiable is warned about, not gated', () => {
    expect(downloadRefusal(volume())).toBeUndefined();
    expect(downloadRefusal(volume({ preflight: { status: 'unverifiable', warning: 'cannot check' } }))).toBeUndefined();
  });
});

describe('cliMessage and isNotDeployed', () => {
  it('recognizes the CLI signature and only it', () => {
    expect(isNotDeployed("volume resolution failed: couldn't find env file: /data/.env")).toBe(true);
    expect(isNotDeployed("couldn't find env file")).toBe(true);
    expect(isNotDeployed('volume list failed: exit status 1')).toBe(false);
  });

  it('strips the panel prefix exactly, and leaves anything else alone', () => {
    expect(cliMessage("volume resolution failed: couldn't find env file: /data/.env")).toBe(
      "couldn't find env file: /data/.env",
    );
    expect(cliMessage('volume list failed: exit status 1')).toBe('volume list failed: exit status 1');
  });
});

describe('serviceUserRefusal', () => {
  it('refuses a name or a bare uid, which the helper image cannot resolve safely', () => {
    expect(serviceUserRefusal('postgres')).toContain('not a numeric uid:gid');
    expect(serviceUserRefusal('1000')).toContain('not a numeric uid:gid');
  });

  it('leaves the numeric uid:gid the API and the helper accept alone', () => {
    expect(serviceUserRefusal('999:998')).toBeUndefined();
    expect(serviceUserRefusal(undefined)).toBeUndefined();
  });
});

describe('orphansView', () => {
  const row = { name: 'demo_old', createdAt: '2026-09-01T00:00:00Z' };

  it('is loading with neither a payload nor an error', () => {
    expect(orphansView(undefined, undefined)).toEqual({ status: 'loading' });
  });

  it('carries the error message', () => {
    expect(orphansView(undefined, 'docker said no')).toEqual({ status: 'error', message: 'docker said no' });
  });

  it('is empty for no orphans', () => {
    expect(orphansView({ orphans: [] })).toEqual({ status: 'empty' });
  });

  it('is ready with the rows otherwise', () => {
    expect(orphansView({ orphans: [row] })).toEqual({ status: 'ready', orphans: [row] });
  });
});
