import { describe, expect, it } from 'vitest';

import type { VulnFinding, VulnImage } from '../data/types';
import { buildComposeReport, buildImageReport, composeBuilds, reportFilename } from './vuln-report';

const image: VulnImage = {
  imageId: 'sha256:ab12cd34ef56',
  refs: ['myapp-web:latest'],
  users: [{ kind: 'service', projectId: 1, environmentId: 2, composeId: 3, service: 'web' }],
  os: 'debian 12.7',
  counts: { critical: 1, high: 1, medium: 1, low: 0, unknown: 0, fixable: 2 },
  scannedAt: '2026-11-04T10:12:00Z',
  attemptedAt: '2026-11-04T10:12:00Z',
  error: '',
};

function finding(over: Partial<VulnFinding>): VulnFinding {
  return {
    vulnId: 'CVE-0000-0000',
    pkgName: 'pkg',
    pkgPath: '',
    installedVersion: '1.0',
    fixedVersion: '',
    severity: 'LOW',
    title: '',
    url: '',
    firstSeenAt: '2026-11-01T00:00:00Z',
    new: false,
    ...over,
  };
}

const findings = [
  finding({ vulnId: 'CVE-2024-2', pkgName: 'openssl', installedVersion: '3.0.11-1', fixedVersion: '3.0.15-1', severity: 'HIGH' }),
  finding({ vulnId: 'CVE-2024-1', pkgName: 'openssl', installedVersion: '3.0.11-1', fixedVersion: '3.0.15-1', severity: 'CRITICAL' }),
  finding({ vulnId: 'CVE-2024-3', pkgName: 'openssl', installedVersion: '3.0.11-1', severity: 'MEDIUM' }),
  finding({ vulnId: 'CVE-2021-23337', pkgName: 'lodash', pkgPath: 'app/package-lock.json', installedVersion: '4.17.20', fixedVersion: '4.17.21', severity: 'HIGH' }),
];

describe('buildImageReport', () => {
  it('heads with the ref and ID and splits fixable from no fix yet, grouped by package', () => {
    expect(buildImageReport(image, findings)).toBe(`# Vulnerability report — myapp-web:latest

Image sha256:ab12cd34ef56 · Scanned 2026-11-04 10:12 UTC · OS debian 12.7
Totals: 1 Critical · 1 High · 1 Medium — 2 fixable

## Fixable

| Package | Path | Installed | Fixed in | Severity | CVEs |
|---|---|---|---|---|---|
| openssl | (os) | 3.0.11-1 | 3.0.15-1 | CRITICAL | CVE-2024-1, CVE-2024-2 |
| lodash | app/package-lock.json | 4.17.20 | 4.17.21 | HIGH | CVE-2021-23337 |

## No fix yet

| Package | Path | Installed | Severity | CVEs |
|---|---|---|---|---|
| openssl | (os) | 3.0.11-1 | MEDIUM | CVE-2024-3 |
`);
  });

  it('lists every fixed version a group names, and escapes pipes', () => {
    const report = buildImageReport(image, [
      finding({ vulnId: 'CVE-1', pkgName: 'a|b', fixedVersion: '1.2' }),
      finding({ vulnId: 'CVE-2', pkgName: 'a|b', fixedVersion: '1.1, 1.3' }),
    ]);
    expect(report).toContain('| a\\|b | (os) | 1.0 | 1.2, 1.1, 1.3 | LOW | CVE-1, CVE-2 |');
  });

  it('says so when nothing was found', () => {
    const report = buildImageReport({ ...image, counts: { critical: 0, high: 0, medium: 0, low: 0, unknown: 0, fixable: 0 } }, []);
    expect(report).toContain('Totals: no findings');
    expect(report).toContain('## Fixable\n\nNone.\n\n## No fix yet\n\nNone.\n');
  });

  it('notes a failed last scan and a never-scanned image', () => {
    expect(buildImageReport({ ...image, error: 'timeout' }, findings)).toContain('Last scan failed: timeout');
    expect(buildImageReport({ ...image, counts: null, scannedAt: null }, [])).toContain('Not scanned yet.');
  });

  it('falls back to the short ID when the image has no ref', () => {
    expect(buildImageReport({ ...image, refs: [] }, [])).toMatch(/^# Vulnerability report — ab12cd34ef56\n/);
  });
});

describe('buildComposeReport', () => {
  it('heads with the Compose and Source, and names the Services and build of each image', () => {
    const report = buildComposeReport({
      title: 'myapp / production / web',
      source: 'git@github.com:me/myapp.git @ a1b2c3d',
      builds: { web: { context: './web', dockerfile: 'Dockerfile.prod' } },
      images: [{ image, findings, services: ['web'] }],
      generatedAt: new Date('2026-11-04T11:00:00Z'),
    });
    expect(report).toMatch(/^# Vulnerability report — myapp \/ production \/ web\n\nGenerated 2026-11-04 11:00 UTC\nSource: git@github.com:me\/myapp.git @ a1b2c3d\n\n## Image `myapp-web:latest`, used by service web\n/);
    expect(report).toContain('Build of web: context ./web, Dockerfile Dockerfile.prod');
    expect(report).toContain('Image sha256:ab12cd34ef56 · Scanned 2026-11-04 10:12 UTC · OS debian 12.7');
    expect(report).toContain('### Fixable');
    expect(report).toContain('### No fix yet');
  });

  it('leaves out the Source line when the Compose has none, and says when no image is scanned', () => {
    const report = buildComposeReport({ title: 'p / e / c', builds: {}, images: [], generatedAt: new Date('2026-11-04T11:00:00Z') });
    expect(report).not.toContain('Source:');
    expect(report).toContain('No image of this Compose has been found in use.');
  });
});

describe('composeBuilds', () => {
  it('reads the short and long build forms', () => {
    expect(
      composeBuilds(`services:
  web:
    build: ./web
  api:
    build:
      context: ./api
      dockerfile: Dockerfile.prod
      target: runtime
  db:
    image: postgres:16
`),
    ).toEqual({
      web: { context: './web' },
      api: { context: './api', dockerfile: 'Dockerfile.prod', target: 'runtime' },
    });
  });

  it('returns nothing for YAML it cannot read', () => {
    expect(composeBuilds('services: [')).toEqual({});
    expect(composeBuilds('')).toEqual({});
  });
});

describe('reportFilename', () => {
  it('keeps safe characters only', () => {
    expect(reportFilename('myapp / production / web')).toBe('vuln-report-myapp-production-web.md');
    expect(reportFilename('ghcr.io/me/app:1.2')).toBe('vuln-report-ghcr.io-me-app-1.2.md');
  });
});
