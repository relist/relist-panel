import { execFileSync } from 'node:child_process';
import { expect, test } from '@playwright/test';
import { dockerEnv, sandbox } from '../lib/sandbox.js';

// The sandbox's Docker API is TLS on a loopback port. A spec that shells out to
// docker must reach it, whichever way it builds its child environment.

test('a docker child process reaches the sandbox the way specs call it', () => {
  const env = sandbox();
  const out = execFileSync('docker', ['version', '--format', '{{.Server.Version}}'], {
    encoding: 'utf8',
    env: { ...process.env, DOCKER_HOST: env.dockerHost, DOCKER_CONFIG: env.dockerConfig },
  }).trim();
  expect(out).toMatch(/^\d+\.\d+/);
});

test('dockerEnv alone is enough', () => {
  const out = execFileSync('docker', ['ps', '--format', '{{.Names}}'], {
    encoding: 'utf8',
    env: { PATH: process.env.PATH!, ...dockerEnv() },
  });
  expect(out).toContain('relist-panel');
});
