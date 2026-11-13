import dns from 'node:dns';
import fs from 'node:fs';
import http from 'node:http';
import https from 'node:https';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

/** This checkout's e2e sandbox, as scripts/e2e-sandbox.sh wrote it. */
export type Sandbox = {
  sandbox: string;
  panelUrl: string;
  httpPort: number;
  httpsPort: number;
  dockerHost: string;
  dockerConfig: string;
  adminEmail: string;
  adminPassword: string;
};

const envFile = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../.data/e2e/env.json');

let cached: Sandbox | undefined;

export function sandbox(): Sandbox {
  if (!cached) {
    if (!fs.existsSync(envFile)) {
      throw new Error(`${envFile} is missing: run "task e2e:serve" first`);
    }
    cached = JSON.parse(fs.readFileSync(envFile, 'utf8')) as Sandbox;
  }
  return cached;
}

export type Reply = { status: number; headers: http.IncomingHttpHeaders; body: string };

export type CallOptions = {
  method?: string;
  headers?: Record<string, string>;
  body?: string;
  /** user:password for Basic auth. */
  auth?: string;
};

// Node does not resolve *.localhost the way browsers and curl do; every
// sandbox host lives on the loopback interface.
const lookup: typeof dns.lookup = ((host: string, options: any, callback: any) => {
  if (typeof options === 'function') {
    callback = options;
    options = {};
  }
  if (host === 'localhost' || host.endsWith('.localhost')) {
    return options?.all ? callback(null, [{ address: '127.0.0.1', family: 4 }]) : callback(null, '127.0.0.1', 4);
  }
  return dns.lookup(host, options, callback);
}) as typeof dns.lookup;

/** One HTTP(S) request; certificates are not checked, redirects not followed. */
export function call(url: string, options: CallOptions = {}): Promise<Reply> {
  const target = new URL(url);
  const client = target.protocol === 'https:' ? https : http;
  const headers: Record<string, string> = { ...options.headers };
  if (options.auth) {
    headers.Authorization = `Basic ${Buffer.from(options.auth).toString('base64')}`;
  }
  return new Promise((resolve, reject) => {
    const req = client.request(
      target,
      { method: options.method ?? 'GET', headers, lookup, rejectUnauthorized: false },
      (res) => {
        let body = '';
        res.setEncoding('utf8');
        res.on('data', (chunk) => (body += chunk));
        res.on('end', () => resolve({ status: res.statusCode ?? 0, headers: res.headers, body }));
      },
    );
    req.on('error', reject);
    if (options.body !== undefined) {
      req.write(options.body);
    }
    req.end();
  });
}

/** A URL on one of the sandbox's Traefik entrypoints for a *.localhost host. */
export function traefikUrl(host: string, scheme: 'http' | 'https', pathname = '/'): string {
  const env = sandbox();
  const port = scheme === 'https' ? env.httpsPort : env.httpPort;
  return `${scheme}://${host}:${port}${pathname}`;
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly body: any,
  ) {
    super(`HTTP ${status}: ${typeof body === 'string' ? body : JSON.stringify(body)}`);
  }
}

/** The panel's JSON API, signed in with a session cookie. */
export class Panel {
  private cookie = '';

  private constructor(private readonly base: string) {}

  static async signIn(email = sandbox().adminEmail, password = sandbox().adminPassword): Promise<Panel> {
    const panel = new Panel(sandbox().panelUrl);
    const reply = await panel.raw('POST', '/api/auth/login', { email, password });
    if (reply.status !== 200) {
      throw new ApiError(reply.status, reply.body);
    }
    const setCookie = reply.headers['set-cookie'] ?? [];
    panel.cookie = setCookie.map((c) => c.split(';')[0]).join('; ');
    return panel;
  }

  /** Any request; the reply is returned whatever its status. */
  raw(method: string, pathname: string, body?: unknown): Promise<Reply> {
    const headers: Record<string, string> = { Origin: this.base };
    if (this.cookie) {
      headers.Cookie = this.cookie;
    }
    if (body !== undefined) {
      headers['Content-Type'] = 'application/json';
    }
    return call(this.base + pathname, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  }

  /** A request that must succeed; its JSON body, or undefined when empty. */
  async json<T = any>(method: string, pathname: string, body?: unknown): Promise<T> {
    const reply = await this.raw(method, pathname, body);
    const parsed = reply.body ? safeJSON(reply.body) : undefined;
    if (reply.status < 200 || reply.status >= 300) {
      throw new ApiError(reply.status, parsed);
    }
    return parsed as T;
  }

  /** Queues a deploy and waits for it to finish; throws unless it succeeded. */
  async deploy(composePath: string, timeoutMs = 4 * 60_000): Promise<number> {
    const { number } = await this.json<{ number: number }>('POST', `${composePath}/deployments`, { mode: 'deploy' });
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      const d = await this.json<{ status: string; reason: string; failedStep: string }>(
        'GET',
        `${composePath}/deployments/${number}`,
      );
      if (d.status === 'succeeded') {
        return number;
      }
      if (d.status === 'failed' || d.status === 'canceled') {
        throw new Error(`deployment ${number} ${d.status} at ${d.failedStep}: ${d.reason}`);
      }
      if (Date.now() > deadline) {
        throw new Error(`deployment ${number} still ${d.status} after ${timeoutMs} ms`);
      }
      await new Promise((r) => setTimeout(r, 1000));
    }
  }
}

function safeJSON(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

/** A short suffix that keeps names unique across runs on one sandbox. */
export function runId(): string {
  return Date.now().toString(36).slice(-6);
}
