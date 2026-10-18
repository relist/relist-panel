import { parse } from 'yaml';
import { describe, expect, it } from 'vitest';

import { insertBuildBlocks } from './build-insert';

describe('insertBuildBlocks', () => {
  it('keeps every comment line', () => {
    const draft = '# top\nservices:\n  # the web\n  web:\n    image: nginx # pinned\n';
    const out = insertBuildBlocks(draft, [
      { service: 'api', context: './api', dockerfile: 'Dockerfile', replaceImage: false },
    ]);
    for (const line of ['# top', '# the web', '# pinned']) {
      expect(out).toContain(line);
    }
  });

  it('appends a new Service with build and restart', () => {
    const out = insertBuildBlocks('services:\n  web:\n    image: nginx\n', [
      { service: 'api', context: './api', dockerfile: 'Dockerfile.prod', replaceImage: false },
    ]);
    expect(parse(out).services.api).toEqual({
      build: { context: './api', dockerfile: 'Dockerfile.prod' },
      restart: 'unless-stopped',
    });
    expect(parse(out).services.web.image).toBe('nginx');
  });

  it('adds target only when given', () => {
    const out = insertBuildBlocks('services: {}\n', [
      { service: 'a', context: '.', dockerfile: 'Dockerfile', target: 'prod', replaceImage: false },
      { service: 'b', context: '.', dockerfile: 'Dockerfile', replaceImage: false },
    ]);
    expect(parse(out).services.a.build.target).toBe('prod');
    expect(parse(out).services.b.build).not.toHaveProperty('target');
  });

  it('keeps an existing image unless replaceImage', () => {
    const draft = 'services:\n  web:\n    image: nginx\n    restart: always\n';
    const kept = parse(
      insertBuildBlocks(draft, [{ service: 'web', context: '.', dockerfile: 'Dockerfile', replaceImage: false }]),
    ).services.web;
    expect(kept.image).toBe('nginx');
    expect(kept.build).toEqual({ context: '.', dockerfile: 'Dockerfile' });
    expect(kept.restart).toBe('always');

    const replaced = parse(
      insertBuildBlocks(draft, [{ service: 'web', context: '.', dockerfile: 'Dockerfile', replaceImage: true }]),
    ).services.web;
    expect(replaced).not.toHaveProperty('image');
    expect(replaced.build).toEqual({ context: '.', dockerfile: 'Dockerfile' });
  });

  it('yields a valid services document from an empty draft', () => {
    const out = insertBuildBlocks('', [{ service: 'api', context: '.', dockerfile: 'Dockerfile', replaceImage: false }]);
    expect(parse(out).services.api.build.context).toBe('.');
  });
});
