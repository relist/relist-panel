import { isMap, isScalar, parseDocument } from 'yaml';

import type { VulnFinding, VulnImage, VulnSeverity } from '../data/types';
import { SEVERITIES } from './vulnerabilities-view';

/**
 * Markdown vulnerability reports for handing to an AI agent: one per Compose
 * (its Source and each image's Services and build) or one per image. Findings
 * are grouped by package, path and installed version, since one upgrade fixes
 * every CVE in a group. The report states facts only; the agent decides.
 */

export interface ServiceBuild {
  context: string;
  dockerfile?: string;
  target?: string;
}

export interface ComposeReportImage {
  image: VulnImage;
  findings: VulnFinding[];
  /** The Compose's Services that run this image. */
  services: string[];
}

export interface ComposeReportInput {
  /** `project / environment / compose`. */
  title: string;
  /** A ready line, e.g. `repoUrl @ commit`; omitted when the Compose has no Source. */
  source?: string;
  builds: Record<string, ServiceBuild>;
  images: ComposeReportImage[];
  generatedAt: Date;
}

const SEVERITY_WORD: Record<VulnSeverity, string> = {
  CRITICAL: 'Critical',
  HIGH: 'High',
  MEDIUM: 'Medium',
  LOW: 'Low',
  UNKNOWN: 'Unknown',
};

interface Row {
  pkgName: string;
  pkgPath: string;
  installed: string;
  fixed: string[];
  severity: VulnSeverity;
  vulnIds: { id: string; severity: VulnSeverity }[];
}

const rank = (s: VulnSeverity) => SEVERITIES.indexOf(s);

function groupRows(findings: VulnFinding[]): Row[] {
  const rows = new Map<string, Row>();
  for (const f of findings) {
    const key = `${f.pkgName}\0${f.pkgPath}\0${f.installedVersion}`;
    let row = rows.get(key);
    if (!row) {
      row = { pkgName: f.pkgName, pkgPath: f.pkgPath, installed: f.installedVersion, fixed: [], severity: f.severity, vulnIds: [] };
      rows.set(key, row);
    }
    for (const v of f.fixedVersion.split(',').map((s) => s.trim()).filter(Boolean)) {
      if (!row.fixed.includes(v)) row.fixed.push(v);
    }
    if (rank(f.severity) < rank(row.severity)) row.severity = f.severity;
    if (!row.vulnIds.some((v) => v.id === f.vulnId)) row.vulnIds.push({ id: f.vulnId, severity: f.severity });
  }
  const sorted = [...rows.values()];
  for (const row of sorted) {
    row.vulnIds.sort((a, b) => rank(a.severity) - rank(b.severity) || a.id.localeCompare(b.id));
  }
  return sorted.sort((a, b) => rank(a.severity) - rank(b.severity) || a.pkgName.localeCompare(b.pkgName));
}

const cell = (s: string) => s.replace(/\|/g, '\\|').replace(/\n/g, ' ');

function table(rows: Row[], fixable: boolean): string {
  if (rows.length === 0) return 'None.\n';
  const head = fixable
    ? '| Package | Path | Installed | Fixed in | Severity | CVEs |\n|---|---|---|---|---|---|\n'
    : '| Package | Path | Installed | Severity | CVEs |\n|---|---|---|---|---|\n';
  return (
    head +
    rows
      .map((r) => {
        const cells = [r.pkgName, r.pkgPath || '(os)', r.installed, ...(fixable ? [r.fixed.join(', ')] : []), r.severity, r.vulnIds.map((v) => v.id).join(', ')];
        return `| ${cells.map(cell).join(' | ')} |\n`;
      })
      .join('')
  );
}

function utc(iso: string | Date): string {
  const d = typeof iso === 'string' ? new Date(iso) : iso;
  return `${d.toISOString().slice(0, 16).replace('T', ' ')} UTC`;
}

const shortId = (imageId: string) => imageId.replace(/^sha256:/, '').slice(0, 12);

/** The facts lines and the two tables, with section headings at `level`. */
function imageBody(image: VulnImage, findings: VulnFinding[], level: number): string {
  const facts = [`Image ${image.imageId}`];
  if (image.scannedAt) facts.push(`Scanned ${utc(image.scannedAt)}`);
  if (image.os) facts.push(`OS ${image.os}`);
  const lines = [facts.join(' · ')];
  if (image.error) lines.push(`Last scan failed: ${image.error}`);
  if (!image.counts) {
    lines.push('Not scanned yet.');
    return `${lines.join('\n')}\n`;
  }
  const c = image.counts;
  const parts = SEVERITIES.map((s) => [s, c[s.toLowerCase() as keyof typeof c]] as const)
    .filter(([, n]) => n > 0)
    .map(([s, n]) => `${n} ${SEVERITY_WORD[s]}`);
  lines.push(parts.length > 0 ? `Totals: ${parts.join(' · ')} — ${c.fixable} fixable` : 'Totals: no findings');

  const h = '#'.repeat(level);
  const fixable = groupRows(findings.filter((f) => f.fixedVersion));
  const unfixed = groupRows(findings.filter((f) => !f.fixedVersion));
  return `${lines.join('\n')}\n\n${h} Fixable\n\n${table(fixable, true)}\n${h} No fix yet\n\n${table(unfixed, false)}`;
}

const imageName = (image: VulnImage) => image.refs[0] ?? shortId(image.imageId);

export function buildImageReport(image: VulnImage, findings: VulnFinding[]): string {
  return `# Vulnerability report — ${imageName(image)}\n\n${imageBody(image, findings, 2)}`;
}

export function buildComposeReport(input: ComposeReportInput): string {
  const head = [`Generated ${utc(input.generatedAt)}`];
  if (input.source) head.push(`Source: ${input.source}`);
  let out = `# Vulnerability report — ${input.title}\n\n${head.join('\n')}\n`;
  if (input.images.length === 0) {
    return `${out}\nNo image of this Compose has been found in use.\n`;
  }
  for (const { image, findings, services } of input.images) {
    const users = services.length === 1 ? `service ${services[0]}` : `services ${services.join(', ')}`;
    const builds = services
      .filter((s) => input.builds[s])
      .map((s) => {
        const b = input.builds[s];
        const parts = [`context ${b.context}`];
        if (b.dockerfile) parts.push(`Dockerfile ${b.dockerfile}`);
        if (b.target) parts.push(`target ${b.target}`);
        return `Build of ${s}: ${parts.join(', ')}\n`;
      })
      .join('');
    out += `\n## Image \`${imageName(image)}\`, used by ${users}\n\n${builds}${imageBody(image, findings, 3)}`;
  }
  return out;
}

/** Each Service's `build:`, short or long form. Unreadable YAML gives none. */
export function composeBuilds(yaml: string): Record<string, ServiceBuild> {
  const out: Record<string, ServiceBuild> = {};
  const doc = parseDocument(yaml);
  if (doc.errors.length > 0) return out;
  const services = doc.get('services');
  if (!isMap(services)) return out;
  for (const item of services.items) {
    const name = isScalar(item.key) ? String(item.key.value) : '';
    const service = item.value;
    if (!name || !isMap(service)) continue;
    const build = service.get('build');
    if (typeof build === 'string') {
      out[name] = { context: build };
    } else if (isMap(build)) {
      const b: ServiceBuild = { context: String(build.get('context') ?? '.') };
      const dockerfile = build.get('dockerfile');
      const target = build.get('target');
      if (typeof dockerfile === 'string') b.dockerfile = dockerfile;
      if (typeof target === 'string') b.target = target;
      out[name] = b;
    }
  }
  return out;
}

export function reportFilename(name: string): string {
  const safe = name.replace(/[^A-Za-z0-9._-]+/g, '-').replace(/^-+|-+$/g, '');
  return `vuln-report-${safe}.md`;
}
