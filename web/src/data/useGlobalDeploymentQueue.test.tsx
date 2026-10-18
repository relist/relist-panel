// @vitest-environment jsdom
import '../test/dom-setup';

import { act, render, screen } from '@testing-library/react';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from './api';
import { useGlobalDeploymentQueue } from './useGlobalDeploymentQueue';

vi.mock('./api', () => ({ api: { globalDeploymentQueue: vi.fn() } }));

function Queue({ enabled = true }: { enabled?: boolean }) {
  const { data } = useGlobalDeploymentQueue(enabled);
  return <span>{data ? `Queue: ${data.length}` : 'Loading'}</span>;
}

async function tick(ms = 0) {
  await act(async () => { await vi.advanceTimersByTimeAsync(ms); });
}

beforeEach(() => { vi.useFakeTimers(); });
afterEach(() => { vi.useRealTimers(); vi.resetAllMocks(); });

describe('useGlobalDeploymentQueue', () => {
  it('shares one queue request between the side menu and page', async () => {
    vi.mocked(api.globalDeploymentQueue).mockResolvedValue([]);
    render(<SWRConfig value={{ provider: () => new Map() }}><Queue /><Queue /></SWRConfig>);
    await tick();
    expect(screen.getAllByText('Queue: 0')).toHaveLength(2);
    expect(api.globalDeploymentQueue).toHaveBeenCalledTimes(1);
  });

  it('does not read the queue before authentication enables it', async () => {
    vi.mocked(api.globalDeploymentQueue).mockResolvedValue([]);
    const result = render(<SWRConfig value={{ provider: () => new Map() }}><Queue enabled={false} /></SWRConfig>);
    await tick(5000);
    expect(api.globalDeploymentQueue).not.toHaveBeenCalled();
    result.rerender(<SWRConfig value={{ provider: () => new Map() }}><Queue /></SWRConfig>);
    await tick();
    expect(api.globalDeploymentQueue).toHaveBeenCalledTimes(1);
  });

  it('refreshes active work then slows down when the queue becomes empty', async () => {
    vi.mocked(api.globalDeploymentQueue)
      .mockResolvedValueOnce([{ status: 'queued' } as never])
      .mockResolvedValue([]);
    render(<SWRConfig value={{ provider: () => new Map() }}><Queue /></SWRConfig>);
    await tick();
    expect(screen.getByText('Queue: 1')).toBeTruthy();
    await tick(1000);
    expect(screen.getByText('Queue: 0')).toBeTruthy();
    expect(api.globalDeploymentQueue).toHaveBeenCalledTimes(2);
    await tick(4000);
    expect(api.globalDeploymentQueue).toHaveBeenCalledTimes(2);
    await tick(1000);
    expect(api.globalDeploymentQueue).toHaveBeenCalledTimes(3);
  });
});
