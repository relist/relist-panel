// @vitest-environment jsdom
import '../test/dom-setup';

import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from './api';
import { PanelProvider, usePanel } from './store';

vi.mock('./api', async (importOriginal) => {
  const original = await importOriginal<typeof import('./api')>();
  return {
    ...original,
    api: {
      authStatus: vi.fn(),
      createSetEntry: vi.fn(),
      deleteSetEntry: vi.fn(),
      getCompose: vi.fn(),
      listComposes: vi.fn(),
      listEnvironments: vi.fn(),
      listProjects: vi.fn(),
      listVariableSets: vi.fn(),
    },
  };
});

const mocked = vi.mocked(api, true);

function wrapper({ children }: { children: ReactNode }) {
  return <PanelProvider>{children}</PanelProvider>;
}

async function loadedPanel() {
  const hook = renderHook(() => usePanel(), { wrapper });
  await act(async () => {
    await hook.result.current.loadProjects();
  });
  return hook;
}

describe('set entry writes', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocked.authStatus.mockResolvedValue({ setupRequired: false, authenticated: true } as never);
    mocked.listProjects.mockResolvedValue([{ id: 1, name: 'p' }] as never);
    mocked.listEnvironments.mockResolvedValue([{ id: 2, name: 'e' }] as never);
    mocked.listComposes.mockResolvedValue([{ id: 7, name: 'a' }, { id: 8, name: 'b' }, { id: 9, name: 'c' }] as never);
    mocked.getCompose.mockImplementation((async ({ composeId }: { composeId: number }) => ({ id: composeId, name: 'x' })) as never);
    mocked.listVariableSets.mockResolvedValue([
      {
        id: 5,
        usedBy: [
          { composeId: 7, composeName: 'a', service: 'api' },
          { composeId: 7, composeName: 'a', service: 'worker' },
          { composeId: 8, composeName: 'b', service: 'api' },
        ],
      },
      { id: 6, usedBy: [{ composeId: 9, composeName: 'c', service: 'api' }] },
    ] as never);
    mocked.createSetEntry.mockResolvedValue({} as never);
    mocked.deleteSetEntry.mockResolvedValue(undefined as never);
  });

  it('refreshes each Compose that binds the set once after a save', async () => {
    const hook = await loadedPanel();
    await act(async () => {
      await hook.result.current.saveSetEntry(2, 5, { key: 'K', value: 'v', secret: false });
    });
    await waitFor(() => expect(mocked.getCompose).toHaveBeenCalledTimes(2));
    expect(mocked.getCompose.mock.calls.map(([location]) => location.composeId).sort()).toEqual([7, 8]);
  });

  it('refreshes the binders after deleting an entry', async () => {
    const hook = await loadedPanel();
    await act(async () => {
      await hook.result.current.deleteSetEntry(2, 6, 1);
    });
    await waitFor(() => expect(mocked.getCompose).toHaveBeenCalledTimes(1));
    expect(mocked.getCompose.mock.calls[0][0].composeId).toBe(9);
  });

  it('does not report a successful write as failed when a refresh fails', async () => {
    mocked.getCompose.mockRejectedValue(new Error('down'));
    const hook = await loadedPanel();
    await act(async () => {
      await hook.result.current.deleteSetEntry(2, 5, 1);
    });
    expect(mocked.deleteSetEntry).toHaveBeenCalled();
  });
});
