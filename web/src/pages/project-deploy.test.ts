import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Compose } from '../data/types';
import { deployFromList } from './project-deploy';

const api = vi.hoisted(() => ({ getSource: vi.fn() }));
vi.mock('../data/api', () => ({ api }));

const compose = { environmentId: 2, id: 7 } as Compose;

describe('deployFromList', () => {
  const enqueue = vi.fn();
  beforeEach(() => {
    vi.clearAllMocks();
    enqueue.mockResolvedValue(5);
  });

  it('builds when the Compose has a Source', async () => {
    api.getSource.mockResolvedValue({ kind: 'git' });
    await expect(deployFromList(compose, 1, enqueue)).resolves.toBe(5);
    expect(api.getSource).toHaveBeenCalledWith({ composeId: 7, environmentId: 2, projectId: 1 });
    expect(enqueue).toHaveBeenCalledWith(7, { mode: 'build' });
  });

  it('deploys when it has none', async () => {
    api.getSource.mockResolvedValue(null);
    await deployFromList(compose, 1, enqueue);
    expect(enqueue).toHaveBeenCalledWith(7, { mode: 'deploy' });
  });

  it('queues nothing when the Source lookup fails', async () => {
    api.getSource.mockRejectedValue(new Error('boom'));
    await expect(deployFromList(compose, 1, enqueue)).rejects.toThrow('boom');
    expect(enqueue).not.toHaveBeenCalled();
  });
});
