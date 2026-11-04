// @vitest-environment jsdom
import '../test/dom-setup';

import { renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { api } from './api';
import type { AccessView } from './api';
import { PanelProvider } from './store';
import { useCan, useIsAdmin, useIsProjectEditor } from './useCan';

afterEach(() => { vi.restoreAllMocks(); });

const member = { id: 2, email: 'm@example.com', isAdmin: false, isOwner: false };
const access: AccessView = {
  projects: [
    { id: 1, role: 'editor', environments: [{ id: 11, role: 'viewer' }, { id: 12, role: 'editor' }] },
    { id: 2, role: null, environments: [{ id: 21, role: 'operator' }] },
  ],
};

const wrapper = ({ children }: { children: ReactNode }) => <PanelProvider>{children}</PanelProvider>;

async function hooks(isAdmin: boolean) {
  vi.spyOn(api, 'authStatus').mockResolvedValue({ authenticated: true, user: { ...member, isAdmin }, access: isAdmin ? null : access });
  const { result } = renderHook(() => ({
    viewer11: useCan(11, 'viewer'),
    operator11: useCan(11, 'operator'),
    editor12: useCan(12, 'editor'),
    operator21: useCan(21, 'operator'),
    editor21: useCan(21, 'editor'),
    unknown: useCan(99, 'viewer'),
    none: useCan(undefined, 'viewer'),
    projectEditor1: useIsProjectEditor(1),
    projectEditor2: useIsProjectEditor(2),
    admin: useIsAdmin(),
  }), { wrapper });
  await waitFor(() => expect(api.authStatus).toHaveBeenCalled());
  await waitFor(() => expect(result.current.admin).toBe(isAdmin));
  return result;
}

describe('useCan', () => {
  it('answers each level from the Environment role', async () => {
    const result = await hooks(false);
    await waitFor(() => expect(result.current.viewer11).toBe(true));
    expect(result.current).toMatchObject({
      viewer11: true, operator11: false, editor12: true, operator21: true, editor21: false, unknown: false, none: false,
      projectEditor1: true, projectEditor2: false, admin: false,
    });
  });

  it('lets an admin do everything', async () => {
    const result = await hooks(true);
    await waitFor(() => expect(result.current.editor21).toBe(true));
    expect(result.current).toMatchObject({
      viewer11: true, operator11: true, editor12: true, unknown: true, projectEditor2: true, admin: true,
    });
  });
});
