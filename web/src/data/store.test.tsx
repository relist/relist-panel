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
      createVariableSet: vi.fn(),
      deleteSetEntry: vi.fn(),
      deleteVariableSet: vi.fn(),
      getCompose: vi.fn(),
      listComposes: vi.fn(),
      listEnvironments: vi.fn(),
      listProjects: vi.fn(),
      listVariableSets: vi.fn(),
      renameVariableSet: vi.fn(),
      updateSetEntry: vi.fn(),
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

describe('Variable Set writes', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocked.authStatus.mockResolvedValue({ setupRequired: false, authenticated: true } as never);
    mocked.listProjects.mockResolvedValue([{ id: 1, name: 'p' }] as never);
    mocked.listEnvironments.mockResolvedValue([{ id: 2, name: 'e' }] as never);
    mocked.listComposes.mockResolvedValue([{ id: 7, name: 'a' }, { id: 8, name: 'b' }] as never);
    mocked.getCompose.mockImplementation((async ({ composeId }: { composeId: number }) => ({ id: composeId, name: 'x' })) as never);
    mocked.listVariableSets.mockResolvedValue([] as never);
    mocked.createSetEntry.mockResolvedValue({} as never);
    mocked.updateSetEntry.mockResolvedValue({} as never);
    mocked.deleteSetEntry.mockResolvedValue(undefined as never);
    mocked.createVariableSet.mockResolvedValue({} as never);
    mocked.renameVariableSet.mockResolvedValue({} as never);
    mocked.deleteVariableSet.mockResolvedValue(undefined as never);
  });

  const location = (composeId: number) => ({ composeId, environmentId: 2, projectId: 1 });

  it('lists the Compose sets under the Compose path', async () => {
    const hook = await loadedPanel();
    await act(async () => {
      await hook.result.current.loadVariableSets(7);
    });
    expect(mocked.listVariableSets).toHaveBeenCalledWith(location(7));
  });

  it('refreshes only this Compose once after an entry save or delete', async () => {
    const hook = await loadedPanel();
    await act(async () => {
      await hook.result.current.saveSetEntry(7, 5, { key: 'K', value: 'v', secret: false });
    });
    expect(mocked.createSetEntry).toHaveBeenCalledWith(location(7), 5, { key: 'K', value: 'v', secret: false });
    await waitFor(() => expect(mocked.getCompose).toHaveBeenCalledTimes(1));
    await act(async () => {
      await hook.result.current.saveSetEntry(7, 5, { key: 'K', value: 'w', secret: false }, 3);
      await hook.result.current.deleteSetEntry(8, 6, 1);
    });
    expect(mocked.updateSetEntry).toHaveBeenCalledWith(location(7), 5, 3, expect.anything());
    expect(mocked.deleteSetEntry).toHaveBeenCalledWith(location(8), 6, 1);
    expect(mocked.getCompose.mock.calls.map(([where]) => where.composeId)).toEqual([7, 7, 8]);
  });

  it('refreshes the Compose after create, rename and delete of a set', async () => {
    const hook = await loadedPanel();
    await act(async () => {
      await hook.result.current.createVariableSet(7, 'db');
      await hook.result.current.renameVariableSet(7, 5, 'database');
      await hook.result.current.deleteVariableSet(7, 5);
    });
    expect(mocked.createVariableSet).toHaveBeenCalledWith(location(7), 'db');
    expect(mocked.renameVariableSet).toHaveBeenCalledWith(location(7), 5, 'database');
    expect(mocked.deleteVariableSet).toHaveBeenCalledWith(location(7), 5);
    expect(mocked.getCompose).toHaveBeenCalledTimes(3);
  });

  it('does not report a successful write as failed when the refresh fails', async () => {
    mocked.getCompose.mockRejectedValue(new Error('down'));
    const hook = await loadedPanel();
    await act(async () => {
      await hook.result.current.deleteSetEntry(7, 5, 1);
    });
    expect(mocked.deleteSetEntry).toHaveBeenCalled();
  });
});
