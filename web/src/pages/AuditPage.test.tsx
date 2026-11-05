// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { MemoryRouter } from 'react-router';
import { SWRConfig } from 'swr';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { api } from '../data/api';
import { AuditPage } from './AuditPage';

afterEach(() => { vi.restoreAllMocks(); });

function show() {
  return render(
    <App>
      <SWRConfig value={{ provider: () => new Map(), revalidateOnFocus: false, shouldRetryOnError: false }}>
        <MemoryRouter>
          <AuditPage />
        </MemoryRouter>
      </SWRConfig>
    </App>,
  );
}

describe('AuditPage', () => {
  it('lists events and saves the retention', async () => {
    const list = vi.spyOn(api, 'listAudit').mockResolvedValue({
      items: [{ id: 1, at: 1_800_000_000_000, actorUserId: 1, actor: 'admin@example.com', action: 'grant.set', outcome: 'ok',
        projectId: null, environmentId: null, composeId: null, target: 'bob@example.com', detail: null, ip: null }],
      nextCursor: null,
    });
    vi.spyOn(api, 'getAuditSettings').mockResolvedValue({ retentionDays: 90, keepForever: false });
    const put = vi.spyOn(api, 'putAuditSettings').mockResolvedValue({ retentionDays: 30, keepForever: false });
    const user = userEvent.setup();
    show();
    expect(await screen.findByRole('heading', { name: 'Audit log' })).toBeTruthy();
    expect(await screen.findByText('grant.set')).toBeTruthy();
    expect(list).toHaveBeenCalledWith({});

    const days = await screen.findByRole('spinbutton', { name: 'Days to keep' });
    await user.clear(days);
    await user.type(days, '30');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(put).toHaveBeenCalledWith({ keepForever: false, retentionDays: 30 }));
  });
});
