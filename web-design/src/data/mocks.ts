import type { ActivityEntry, Compose, Environment, EnvironmentVariable, HostInfo, MetricSample, MetricWindow, Project, ServiceSample } from './types';
import { findCompose, METRIC_WINDOWS, SERVICE_METRIC_WINDOWS } from './types';

/**
 * Hand-written fixtures. Deliberately messy in the ways a real single host is:
 * one project with several environments, one compose only partly running, one
 * that has never been deployed, and a project with nothing in it yet.
 */

export const host: HostInfo = {
  name: 'relist-host',
  dockerVersion: '27.3.1',
  traefikVersion: 'v3.1',
  dataDir: '/var/lib/relist-panel',
  network: 'relist-network',
};

// Fixture timestamps are relative to load time, so relative copy always reads
// as the past ("43 minutes ago") no matter when the prototype is opened.
const now = new Date();
const ago = (minutes: number) => new Date(now.getTime() - minutes * 60_000).toISOString();
const daysAgo = (days: number) => ago(days * 24 * 60);

/**
 * A short container ID. What mints one is a deploy, so the seed is the deploy
 * (or, for fixtures, the Compose and service it belongs to): two deploys of the
 * same service get different IDs, which is why the Monitor tab keys history by
 * service name and shows the container ID as identity for one deploy only.
 */
export function containerIdFor(seed: string): string {
  const part = (channel: number) => {
    let value = 2_166_136_261;
    for (let index = 0; index < seed.length; index += 1) {
      value = Math.imul(value ^ seed.charCodeAt(index), 16_777_619);
    }
    value = Math.imul(value ^ channel, 2_246_822_519);
    value ^= value >>> 13;
    return (value >>> 0).toString(16).padStart(8, '0');
  };
  return `${part(0x5f3a)}${part(0x91c7)}`.slice(0, 12);
}

const saasWebYAML = `# my-saas / production / web
services:
  api:
    image: ghcr.io/relist/my-saas-api:2.14.0
    restart: unless-stopped
    environment:
      DATABASE_URL: \${DATABASE_URL}
      REDIS_URL: \${REDIS_URL}
      LOG_LEVEL: \${LOG_LEVEL}
    labels:
      - traefik.enable=true
      - traefik.http.routers.my-saas-api.rule=Host(\`api.my-saas.dev\`)
      - traefik.http.services.my-saas-api.loadbalancer.server.port=8080
    depends_on:
      - redis

  redis:
    image: redis:7.4-alpine
    restart: unless-stopped
    command: ["redis-server", "--appendonly", "yes"]
    volumes:
      - redis-data:/data

volumes:
  redis-data:
`;

const saasWorkerYAML = `# my-saas / production / worker
services:
  worker:
    image: ghcr.io/relist/my-saas-worker:2.14.0
    restart: unless-stopped
    command: ["node", "dist/worker.js"]
    environment:
      DATABASE_URL: \${DATABASE_URL}
      REDIS_URL: \${REDIS_URL}

  worker-cron:
    image: ghcr.io/relist/my-saas-worker:2.14.0
    restart: "no"
    command: ["node", "dist/cron.js"]
    environment:
      DATABASE_URL: \${DATABASE_URL}
      REDIS_URL: \${REDIS_URL}
`;

const saasGrafanaYAML = `# my-saas / production / metrics
services:
  grafana:
    image: grafana/grafana:11.2.0
    restart: unless-stopped
    environment:
      GF_SECURITY_ADMIN_PASSWORD: \${GRAFANA_PASSWORD}
      GF_SERVER_ROOT_URL: https://grafana.my-saas.dev
    labels:
      - traefik.enable=true
      - traefik.http.routers.my-saas-grafana.rule=Host(\`grafana.my-saas.dev\`)
      - traefik.http.services.my-saas-grafana.loadbalancer.server.port=3000
    volumes:
      - grafana-data:/var/lib/grafana

volumes:
  grafana-data:
`;

const landingSiteYAML = `# landing / production / site
services:
  site:
    image: ghcr.io/relist/landing:1.8.3
    restart: unless-stopped
    labels:
      - traefik.enable=true
      - traefik.http.routers.landing.rule=Host(\`relist.dev\`) || Host(\`www.relist.dev\`)
      - traefik.http.services.landing.loadbalancer.server.port=80
`;

const toolsYAML = `# internal-tools / production / uptime
services:
  uptime:
    image: louislam/uptime-kuma:1.23.13
    restart: unless-stopped
    volumes:
      - uptime-data:/app/data
    labels:
      - traefik.enable=true
      - traefik.http.routers.uptime.rule=Host(\`status.relist.dev\`)
      - traefik.http.services.uptime.loadbalancer.server.port=3001

volumes:
  uptime-data:
`;

const blogYAML = `# blog / production / ghost
services:
  ghost:
    image: ghost:5-alpine
    restart: unless-stopped
    environment:
      url: https://blog.relist.dev
      database__client: sqlite3
    volumes:
      - ghost-content:/var/lib/ghost/content

volumes:
  ghost-content:
`;

function variable(
  id: number,
  environmentId: number,
  key: string,
  value: string,
  minutesAgo: number,
  secret = false,
): EnvironmentVariable {
  return { id, environmentId, key, value, secret, updatedAt: ago(minutesAgo) };
}

const saasProductionVariables = [
  variable(1, 1, 'DATABASE_URL', 'postgres://app:9f2b1c@db.internal:5432/my_saas', 2_880, true),
  variable(2, 1, 'REDIS_URL', 'redis://redis.internal:6379/0', 2_880),
  variable(3, 1, 'JWT_SECRET', 'sk_live_4f9a2c7e81bd45f0', 43, true),
  variable(4, 1, 'SMTP_URL', 'smtp://mail.relist.dev:587', 10_080),
  variable(5, 1, 'LOG_LEVEL', 'info', 43),
  variable(6, 1, 'TZ', 'Asia/Bangkok', 20_160),
];

const saasStagingVariables = [
  variable(7, 2, 'DATABASE_URL', 'postgres://app:staging@db.internal:5433/my_saas_staging', 1_440, true),
  variable(8, 2, 'REDIS_URL', 'redis://redis.internal:6380/0', 1_440),
  variable(9, 2, 'LOG_LEVEL', 'debug', 300),
];

const landingVariables = [variable(10, 4, 'TZ', 'Asia/Bangkok', 40_320)];

const toolsVariables = [
  variable(11, 5, 'TZ', 'Asia/Bangkok', 26_000),
  variable(12, 5, 'UPTIME_SECRET', 'upt_kuma_7c21ab', 900, true),
];

const blogVariables = [variable(13, 6, 'TZ', 'Asia/Bangkok', 60_000)];

function compose(input: Omit<Compose, 'createdAt' | 'updatedAt'> & { updatedMinutesAgo: number }): Compose {
  const { updatedMinutesAgo, ...rest } = input;
  return { ...rest, createdAt: daysAgo(60), updatedAt: ago(updatedMinutesAgo) };
}

const environments: Environment[] = [
  {
    id: 1,
    projectId: 1,
    name: 'production',
    createdAt: daysAgo(120),
    variables: saasProductionVariables,
    composes: [
      compose({
        id: 1,
        environmentId: 1,
        name: 'web',
        projectSlug: 'my-saas',
        envSlug: 'production',
        yamlContent: saasWebYAML,
        updatedMinutesAgo: 43,
        lastDeployedAt: ago(41),
        lastAction: 'deployed',
        status: { running: 2, total: 2 },
        services: [
          { name: 'api', image: 'ghcr.io/relist/my-saas-api:2.14.0', state: 'running', health: 'healthy', ports: ['8080/tcp'], containerId: containerIdFor('1-api') },
          { name: 'redis', image: 'redis:7.4-alpine', state: 'running', health: 'none', ports: ['6379/tcp'], containerId: containerIdFor('1-redis') },
        ],
      }),
      compose({
        id: 2,
        environmentId: 1,
        name: 'worker',
        projectSlug: 'my-saas',
        envSlug: 'production',
        yamlContent: saasWorkerYAML,
        updatedMinutesAgo: 43,
        lastDeployedAt: ago(41),
        lastAction: 'deployed',
        status: { running: 1, total: 2 },
        services: [
          { name: 'worker', image: 'ghcr.io/relist/my-saas-worker:2.14.0', state: 'running', health: 'none', ports: [], containerId: containerIdFor('2-worker') },
          { name: 'worker-cron', image: 'ghcr.io/relist/my-saas-worker:2.14.0', state: 'exited', health: 'none', ports: [], containerId: containerIdFor('2-worker-cron') },
        ],
      }),
      compose({
        id: 3,
        environmentId: 1,
        name: 'metrics',
        projectSlug: 'my-saas',
        envSlug: 'production',
        yamlContent: saasGrafanaYAML,
        updatedMinutesAgo: 2_880,
        status: { running: 0, total: 0 },
        services: [],
      }),
    ],
  },
  {
    id: 2,
    projectId: 1,
    name: 'staging',
    createdAt: daysAgo(90),
    variables: saasStagingVariables,
    composes: [
      compose({
        id: 4,
        environmentId: 2,
        name: 'web',
        projectSlug: 'my-saas',
        envSlug: 'staging',
        yamlContent: saasWebYAML.replace('my-saas-api.rule', 'my-saas-staging-api.rule'),
        updatedMinutesAgo: 300,
        lastDeployedAt: ago(299),
        lastAction: 'deployed',
        status: { running: 2, total: 2 },
        services: [
          { name: 'api', image: 'ghcr.io/relist/my-saas-api:2.15.0-rc1', state: 'running', health: 'starting', ports: ['8080/tcp'], containerId: containerIdFor('4-api') },
          { name: 'redis', image: 'redis:7.4-alpine', state: 'running', health: 'none', ports: ['6379/tcp'], containerId: containerIdFor('4-redis') },
        ],
      }),
    ],
  },
  {
    id: 3,
    projectId: 1,
    name: 'preview',
    createdAt: daysAgo(14),
    variables: [],
    composes: [],
  },
  {
    id: 4,
    projectId: 2,
    name: 'production',
    createdAt: daysAgo(200),
    variables: landingVariables,
    composes: [
      compose({
        id: 5,
        environmentId: 4,
        name: 'site',
        projectSlug: 'landing',
        envSlug: 'production',
        yamlContent: landingSiteYAML,
        updatedMinutesAgo: 1_500,
        lastDeployedAt: ago(1_499),
        lastAction: 'deployed',
        status: { running: 1, total: 1 },
        services: [{ name: 'site', image: 'ghcr.io/relist/landing:1.8.3', state: 'running', health: 'none', ports: ['80/tcp'], containerId: containerIdFor('5-site') }],
      }),
    ],
  },
  {
    id: 5,
    projectId: 3,
    name: 'production',
    createdAt: daysAgo(75),
    variables: toolsVariables,
    composes: [
      compose({
        id: 6,
        environmentId: 5,
        name: 'uptime',
        projectSlug: 'internal-tools',
        envSlug: 'production',
        yamlContent: toolsYAML,
        updatedMinutesAgo: 4_320,
        lastDeployedAt: ago(4_318),
        lastAction: 'deployed',
        status: { running: 1, total: 1 },
        services: [{ name: 'uptime', image: 'louislam/uptime-kuma:1.23.13', state: 'running', health: 'none', ports: ['3001/tcp'], containerId: containerIdFor('6-uptime') }],
      }),
      compose({
        id: 7,
        environmentId: 5,
        name: 'registry',
        projectSlug: 'internal-tools',
        envSlug: 'production',
        yamlContent: blogYAML.replace(/ghost/g, 'registry'),
        updatedMinutesAgo: 11_000,
        lastDeployedAt: ago(10_990),
        lastAction: 'stopped',
        status: { running: 0, total: 1 },
        services: [{ name: 'registry', image: 'registry:2.8', state: 'exited', health: 'none', ports: [], containerId: containerIdFor('7-registry') }],
      }),
    ],
  },
  {
    id: 6,
    projectId: 4,
    name: 'production',
    createdAt: daysAgo(310),
    variables: blogVariables,
    composes: [
      compose({
        id: 8,
        environmentId: 6,
        name: 'ghost',
        projectSlug: 'blog',
        envSlug: 'production',
        yamlContent: blogYAML,
        updatedMinutesAgo: 260,
        status: { running: 0, total: 0 },
        services: [],
      }),
    ],
  },
];

export const projects: Project[] = [
  {
    id: 1,
    name: 'my-saas',
    description: 'Customer-facing SaaS app: API, background worker and the Grafana dashboard operators watch during incidents.',
    environmentCount: 3,
    createdAt: daysAgo(120),
    lastUpdated: ago(43),
    environments: environments.filter((environment) => environment.projectId === 1),
  },
  {
    id: 2,
    name: 'landing',
    description: 'Marketing site served by Traefik at relist.dev.',
    environmentCount: 1,
    createdAt: daysAgo(200),
    lastUpdated: ago(1_500),
    environments: environments.filter((environment) => environment.projectId === 2),
  },
  {
    id: 3,
    name: 'internal-tools',
    description: 'Self-hosted operator tooling. No public routing.',
    environmentCount: 1,
    createdAt: daysAgo(75),
    lastUpdated: ago(4_320),
    environments: environments.filter((environment) => environment.projectId === 3),
  },
  {
    id: 4,
    name: 'blog',
    description: '',
    environmentCount: 1,
    createdAt: daysAgo(310),
    lastUpdated: ago(260),
    environments: environments.filter((environment) => environment.projectId === 4),
  },
];

export const activity: ActivityEntry[] = [
  {
    id: 1,
    at: ago(41),
    actor: 'admin@relist.local',
    action: 'compose.deploy',
    detail: 'Deployed my-saas / production / web',
    target: '#/projects/1/env/1/compose/1',
    ok: true,
  },
  {
    id: 2,
    at: ago(43),
    actor: 'admin@relist.local',
    action: 'compose.save',
    detail: 'Saved docker-compose.yml for my-saas / production / web',
    target: '#/projects/1/env/1/compose/1',
    ok: true,
  },
  {
    id: 3,
    at: ago(299),
    actor: 'admin@relist.local',
    action: 'compose.deploy',
    detail: 'Deployed my-saas / staging / web',
    target: '#/projects/1/env/2/compose/4',
    ok: true,
  },
  {
    id: 4,
    at: ago(1_499),
    actor: 'admin@relist.local',
    action: 'compose.deploy',
    detail: 'Deployed landing / production / site',
    target: '#/projects/2/env/4/compose/5',
    ok: true,
  },
  {
    id: 5,
    at: ago(10_990),
    actor: 'admin@relist.local',
    action: 'compose.stop',
    detail: 'Stopped internal-tools / production / registry',
    target: '#/projects/3/env/5/compose/7',
    ok: true,
  },
  {
    id: 6,
    at: ago(11_005),
    actor: 'admin@relist.local',
    action: 'compose.restart',
    detail: 'Restart failed for internal-tools / production / registry',
    target: '#/projects/3/env/5/compose/7',
    ok: false,
  },
];

export const composeLogs: Record<number, string> = {
  1: [
    'redis  | 1:M 23 Sep 2026 08:31:02.114 * Ready to accept connections tcp',
    'api    | {"level":30,"time":1758616262114,"msg":"listening","port":8080}',
    'api    | {"level":30,"time":1758616262387,"msg":"database connected","pool":10}',
    'api    | {"level":30,"time":1758616264001,"msg":"request","method":"GET","url":"/healthz","status":200,"ms":2}',
    'api    | {"level":40,"time":1758616320955,"msg":"slow query","model":"Invoice","ms":1840}',
    'api    | {"level":30,"time":1758616390002,"msg":"request","method":"POST","url":"/v1/invoices","status":201,"ms":64}',
    'redis  | 1:M 23 Sep 2026 08:53:11.902 * Background saving started by pid 42',
    'redis  | 1:M 23 Sep 2026 08:53:12.418 * Background saving terminated with success',
    'api    | {"level":50,"time":1758616500112,"msg":"upstream timeout","target":"smtp://mail.relist.dev:587","ms":5000}',
    'api    | {"level":30,"time":1758616501000,"msg":"retrying","attempt":1,"target":"smtp"}',
    'api    | {"level":30,"time":1758616501820,"msg":"email queued","id":"em_8f21","attempt":2}',
    'api    | {"level":30,"time":1758616812345,"msg":"request","method":"GET","url":"/v1/projects","status":200,"ms":11}',
  ].join('\n'),
  2: [
    'worker      | {"level":30,"msg":"worker started","queue":"invoices"}',
    'worker      | {"level":30,"msg":"processed job","id":"job_9912","ms":412}',
    'worker-cron | {"level":30,"msg":"cron schedule loaded","jobs":4}',
    'worker-cron | {"level":50,"msg":"fatal: ENOENT /app/secrets/gcp.json"}',
    'worker-cron | process exited with code 1',
  ].join('\n'),
  4: [
    'api   | {"level":30,"msg":"listening","port":8080,"env":"staging"}',
    'redis | 1:M 23 Sep 2026 04:12:00.001 * Ready to accept connections tcp',
  ].join('\n'),
  5: 'site  | nginx/1.27.1 started\nsite  | 172.18.0.1 - - [22/Sep/2026:14:02:11 +0000] "GET / HTTP/1.1" 200 8412',
  6: 'uptime | Listening on 3001\nuptime | [DB] connected to sqlite',
  7: 'registry | time="2026-09-20T02:11:04Z" level=info msg="listening on [::]:5000"',
  8: 'ghost | Ghost 5.92.0 is running\nghost | sqlite3 connected',
};

/**
 * Host metrics are generated, not hand-written: six series across four windows
 * would be thousands of literal numbers, and reviewers need the same picture
 * twice. Every value is a pure function of the sample's own timestamp — an
 * integer hash, never `Math.random` — so the same wall-clock second reads the
 * same in every window and screenshots stay reproducible.
 */

/** In-memory history: a panel that restarted 3 hours ago has exactly this much. */
export const PANEL_HISTORY_MINUTES = 187;

const GIB = 1024 ** 3;
const MEMORY_TOTAL = 32 * GIB;
const DISK_TOTAL = 512 * GIB;
const DOCKER_TOTAL = 180 * GIB;

/** 32-bit integer hash of (second, channel) → 0..1. */
function noise(at: number, channel: number): number {
  let value = Math.floor(at / 1_000) | 0;
  value = Math.imul(value ^ channel, 2_246_822_519);
  value ^= value >>> 13;
  value = Math.imul(value, 3_266_489_917);
  value ^= value >>> 16;
  return (value >>> 0) / 4_294_967_296;
}

const jitter = (at: number, channel: number) => noise(at, channel) - 0.5;

/** A gaussian bump — how a deploy, a backup or a cache flush shows up. */
const bump = (minutesAgo: number, center: number, width: number) =>
  Math.exp(-(((minutesAgo - center) / width) ** 2));

/** Sawtooth 0..1 that climbs as `minutesAgo` falls, dropping once per period. */
const sawtooth = (minutesAgo: number, period: number, dropAt: number) =>
  ((((dropAt - minutesAgo) % period) + period) % period) / period;

const clamp = (value: number, min: number, max: number) => Math.min(max, Math.max(min, value));

const hourOfDay = (at: number) => {
  const date = new Date(at);
  return date.getHours() + date.getMinutes() / 60 + date.getSeconds() / 3_600;
};

export function metricSamples(window: MetricWindow, now = Date.now()): MetricSample[] {
  const { intervalSeconds, windowSeconds } = METRIC_WINDOWS[window];
  const intervalMs = intervalSeconds * 1_000;
  // Older than the panel's own uptime there is nothing to return.
  const count = Math.min(windowSeconds / intervalSeconds, Math.floor((PANEL_HISTORY_MINUTES * 60) / intervalSeconds));

  return Array.from({ length: count }, (_, index) => {
    const at = now - (count - 1 - index) * intervalMs;
    const minutesAgo = (now - at) / 60_000;

    // A working day, not a flat line: quiet at night, busy after lunch, with a
    // deploy and a backup the operator would want to correlate.
    const cpu = clamp(
      26 +
        22 * Math.sin(((hourOfDay(at) - 6) / 24) * 2 * Math.PI) +
        jitter(at, 1) * 7 +
        38 * bump(minutesAgo, 12, 1.2) +
        22 * bump(minutesAgo, 47, 2.5),
      2,
      99,
    );

    // Fills until something frees it — the one shape that is a story, and the
    // reading that reaches the 85% warning tone.
    const memPercent = clamp(60 + sawtooth(minutesAgo, 46, 45) * 28 + jitter(at, 2) * 1.4, 4, 99);

    // Capacity crawls: one step where logs grew, one where an image was pruned.
    const diskUsed =
      (316 + 3.1 * ((PANEL_HISTORY_MINUTES - minutesAgo) / PANEL_HISTORY_MINUTES) + (minutesAgo < 47 ? 1.8 : 0)) * GIB;
    const dockerUsed =
      (74 + (minutesAgo < 90 ? 0 : 6.2) + 0.4 * ((PANEL_HISTORY_MINUTES - minutesAgo) / PANEL_HISTORY_MINUTES)) * GIB;

    const ioBurst =
      3.2e6 * bump(minutesAgo, 12, 1.1) + 1.5e6 * bump(minutesAgo, 47, 2.2) + 8e5 * bump(minutesAgo % 20, 2, 1.3);
    const netBurst = 2.6e6 * bump(minutesAgo, 12, 1.3) + 7e5 * bump(minutesAgo % 30, 3, 1.8);

    return {
      at,
      blkRead: Math.round(240_000 + jitter(at, 3) * 120_000 + ioBurst),
      blkWrite: Math.round(150_000 + jitter(at, 4) * 80_000 + ioBurst * 0.55),
      cpu: Math.round(cpu * 10) / 10,
      diskTotal: DISK_TOTAL,
      diskUsed: Math.round(diskUsed),
      dockerTotal: DOCKER_TOTAL,
      dockerUsed: Math.round(dockerUsed),
      memTotal: MEMORY_TOTAL,
      memUsed: Math.round((MEMORY_TOTAL * memPercent) / 100),
      netRx: Math.round(1.5e6 + jitter(at, 5) * 500_000 + netBurst),
      netTx: Math.round(650_000 + jitter(at, 6) * 220_000 + netBurst * 0.35),
    };
  });
}

/**
 * Per-service metrics, generated under the same rule as the host series: every
 * value is a pure function of the sample's own timestamp plus a per-service
 * seed, never `Math.random`, so the four windows agree with each other and a
 * screenshot taken twice shows the same picture.
 *
 * The shape follows the role the service plays, because that is what an
 * operator is looking for: a web tier that is mostly flat with traffic bursts,
 * a worker whose CPU sawtooths with each job, a database that owns memory and
 * barely touches the CPU, a cache that is almost invisible on every axis.
 */

/** A day of 1-minute rollups, so a redeploy never resets what a service has done. */
export const SERVICE_HISTORY_MINUTES = 24 * 60;

const MIB = 1024 ** 2;

type ServiceRole = 'db' | 'redis' | 'web' | 'worker';

function serviceRole(name: string, image: string): ServiceRole {
  const text = `${name} ${image}`.toLowerCase();
  if (text.includes('redis') || text.includes('memcach')) return 'redis';
  if (text.includes('postgres') || text.includes('mysql') || text.includes('mariadb') || text.includes('mongo')) return 'db';
  if (text.includes('worker') || text.includes('cron') || text.includes('queue')) return 'worker';
  return 'web';
}

/**
 * Each role's resting point and swing. A web service peaks near one core, a
 * worker near one and a half — which is why the container CPU chart takes its
 * axis from the data instead of the host's fixed 0–100.
 */
const SERVICE_ROLES = {
  db: { blkBase: 420_000, cpuBase: 9, cpuSwing: 5, memBase: 0.7, memLimit: 8 * GIB, memSwing: 0.12, netBase: 320_000, netBurst: 900_000, pids: 48 },
  redis: { blkBase: 4_000, cpuBase: 0.6, cpuSwing: 0.5, memBase: 0.075, memLimit: 512 * MIB, memSwing: 0.02, netBase: 14_000, netBurst: 120_000, pids: 5 },
  web: { blkBase: 120_000, cpuBase: 22, cpuSwing: 14, memBase: 0.19, memLimit: 2 * GIB, memSwing: 0.05, netBase: 900_000, netBurst: 3.4e6, pids: 14 },
  worker: { blkBase: 220_000, cpuBase: 5, cpuSwing: 145, memBase: 0.26, memLimit: 1 * GIB, memSwing: 0.06, netBase: 45_000, netBurst: 900_000, pids: 9 },
} as const satisfies Record<ServiceRole, {
  blkBase: number;
  cpuBase: number;
  cpuSwing: number;
  memBase: number;
  memLimit: number;
  memSwing: number;
  netBase: number;
  netBurst: number;
  pids: number;
}>;

/** Per-service channel offset: two containers must not draw the same jitter. */
function serviceSeed(text: string): number {
  let hash = 0;
  for (let index = 0; index < text.length; index += 1) {
    hash = (Math.imul(hash, 31) + text.charCodeAt(index)) | 0;
  }
  return (hash >>> 0) % 4_096;
}

export function serviceMetricSamples(
  composeId: number,
  serviceName: string,
  window: MetricWindow,
  now = Date.now(),
): ServiceSample[] {
  const compose = findCompose(projects, composeId);
  const service = compose?.services.find((candidate) => candidate.name === serviceName);
  if (!compose || !service || service.state === 'created') return [];

  const { intervalSeconds, windowSeconds } = SERVICE_METRIC_WINDOWS[window];
  const intervalMs = intervalSeconds * 1_000;
  const role = serviceRole(service.name, service.image);
  const shape = SERVICE_ROLES[role];
  const seed = serviceSeed(`${compose.id}-${service.name}`);
  const deployedAt = compose.lastDeployedAt ? Date.parse(compose.lastDeployedAt) : undefined;
  const deployMinutesAgo = deployedAt === undefined ? undefined : (now - deployedAt) / 60_000;
  // The deploy is the one event the operator already knows the time of, so the
  // generator spikes on it: a chart with a marker on it should have something
  // to show under that marker.
  const deployBump = (minutesAgo: number, width: number) =>
    deployMinutesAgo === undefined ? 0 : bump(minutesAgo, deployMinutesAgo, width);

  // A container that has stopped stops producing samples. Its line keeps the
  // history it has instead of flatlining to zero for the rest of the window.
  const stoppedAt = service.state === 'exited' ? deployedAt : undefined;
  const start = Math.max(now - windowSeconds * 1_000, now - SERVICE_HISTORY_MINUTES * 60_000);
  const end = Math.min(now, stoppedAt ?? now);
  const count = end <= start ? 0 : Math.floor((end - start) / intervalMs) + 1;

  return Array.from({ length: count }, (_, index) => {
    const at = start + index * intervalMs;
    const minutesAgo = (now - at) / 60_000;
    const jitterAt = (channel: number) => jitter(at, channel + seed);

    // Worker CPU is a sawtooth — jobs queue, run hot, and the queue drains.
    const cpu =
      role === 'worker'
        ? shape.cpuBase + shape.cpuSwing * sawtooth(minutesAgo, 15, 14) + jitterAt(1) * 4 + 22 * deployBump(minutesAgo, 1.5)
        : role === 'web'
          ? shape.cpuBase +
            shape.cpuSwing * 0.5 * Math.sin(((hourOfDay(at) - 6) / 24) * 2 * Math.PI) +
            jitterAt(1) * 5 +
            shape.cpuSwing * bump(minutesAgo % 24, 3, 1.2) +
            40 * deployBump(minutesAgo, 1.5)
          : shape.cpuBase + shape.cpuSwing * (0.5 + 0.5 * Math.sin(minutesAgo / 7)) + jitterAt(1) * 2 + 12 * deployBump(minutesAgo, 1.5);

    // A database fills until something frees it; everything else drifts.
    const memPercent = clamp(
      shape.memBase +
        shape.memSwing * (role === 'db' ? sawtooth(minutesAgo, 90, 88) : Math.sin(minutesAgo / 47)) +
        jitterAt(2) * 0.01,
      0.02,
      0.97,
    );

    // The deploy's image pull and the traffic that follows it: one burst, two
    // charts — the correlation the marker exists to make readable.
    const netBurst = shape.netBurst * (bump(minutesAgo % 24, 3, 1.4) + 1.6 * deployBump(minutesAgo, 1.5));
    const netRx = shape.netBase + jitterAt(3) * shape.netBase * 0.3 + netBurst;
    const blkBurst = shape.blkBase * (2.5 * bump(minutesAgo % 46, 2, 1.6) + 3 * deployBump(minutesAgo, 1.5));

    return {
      at,
      blkRead: Math.round(shape.blkBase * (1 + jitterAt(5) * 0.4) + blkBurst),
      blkWrite: Math.round(shape.blkBase * 0.6 * (1 + jitterAt(6) * 0.4) + blkBurst * 0.7),
      cpu: Math.round(clamp(cpu, 0.1, 400) * 10) / 10,
      memLimit: shape.memLimit,
      memUsed: Math.round(shape.memLimit * memPercent),
      netRx: Math.round(netRx),
      netTx: Math.round(netRx * (role === 'redis' ? 1.4 : 0.45) + jitterAt(4) * shape.netBase * 0.15),
      pids: Math.max(1, Math.round(shape.pids + jitterAt(7) * Math.max(1, shape.pids * 0.15))),
    };
  });
}
