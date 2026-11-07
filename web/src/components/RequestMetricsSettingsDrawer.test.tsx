// @vitest-environment jsdom
import '../test/dom-setup';

import { fireEvent, render, screen } from '@testing-library/react';
import { App } from 'antd';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { api } from '../data/api';
import { RequestMetricsSettingsDrawer } from './RequestMetricsSettingsDrawer';

const RANGE_ERROR = 'Enter a whole number of days from 1 to 90.';

function renderDrawer() {
  return render(
    <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
      <App>
        <RequestMetricsSettingsDrawer onClose={() => {}} onSaved={() => {}} open />
      </App>
    </SWRConfig>,
  );
}

async function typeDays(value: string) {
  await screen.findByDisplayValue('7');
  const input = screen.getByLabelText('Days of hourly history to keep') as HTMLInputElement;
  fireEvent.change(input, { target: { value } });
  fireEvent.blur(input);
  return input;
}

describe('RequestMetricsSettingsDrawer', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    vi.spyOn(api, 'requestMetricSettings').mockResolvedValue({ retentionDays: 7 });
  });

  it.each(['0', '91'])('keeps %s as typed, says why under the field, and blocks Save', async (value) => {
    renderDrawer();
    const input = await typeDays(value);
    expect(input.value).toBe(value);
    expect(screen.getByText(RANGE_ERROR)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Save' }).hasAttribute('disabled')).toBe(true);
  });

  it('shows no error for a value in range', async () => {
    renderDrawer();
    await typeDays('30');
    expect(screen.queryByText(RANGE_ERROR)).toBeNull();
    expect(screen.getByRole('button', { name: 'Save' }).hasAttribute('disabled')).toBe(false);
  });
});
