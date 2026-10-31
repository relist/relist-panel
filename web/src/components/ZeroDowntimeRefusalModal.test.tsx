// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { ZeroDowntimeRefusalModal } from './ZeroDowntimeRefusalModal';

const problems = [
  { service: 'web', reasons: ['has no healthcheck', 'sets container_name; two Containers can\'t share a name'] },
  { service: 'api', reasons: ['publishes host port 8080; two Containers can\'t bind it'] },
];

describe('ZeroDowntimeRefusalModal', () => {
  it('lists each service — reason and calls each button’s handler', async () => {
    const onOpenEditor = vi.fn();
    const onOpenDomains = vi.fn();
    const onClose = vi.fn();
    const user = userEvent.setup();
    render(<ZeroDowntimeRefusalModal onClose={onClose} onOpenDomains={onOpenDomains} onOpenEditor={onOpenEditor} problems={problems} />);
    const dialog = await screen.findByRole('dialog', { name: "Can't deploy with zero downtime" });
    expect(within(dialog).getByText('web — has no healthcheck')).toBeTruthy();
    expect(within(dialog).getByText("web — sets container_name; two Containers can't share a name")).toBeTruthy();
    expect(within(dialog).getByText("api — publishes host port 8080; two Containers can't bind it")).toBeTruthy();
    await user.click(within(dialog).getByRole('button', { name: 'Open editor' }));
    await user.click(within(dialog).getByRole('button', { name: 'Open domains' }));
    await user.click(within(dialog).getByRole('button', { name: 'Close' }));
    expect(onOpenEditor).toHaveBeenCalledTimes(1);
    expect(onOpenDomains).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalled();
  });

  it('links to the Compose instead when it has no editor or domains to open', async () => {
    const onOpen = vi.fn();
    const user = userEvent.setup();
    render(<ZeroDowntimeRefusalModal compose={{ name: 'Compose A', onOpen }} onClose={vi.fn()} problems={problems} />);
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).queryByRole('button', { name: 'Open editor' })).toBeNull();
    expect(within(dialog).queryByRole('button', { name: 'Open domains' })).toBeNull();
    await user.click(within(dialog).getByRole('button', { name: 'Open Compose A' }));
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it('is closed without problems', () => {
    render(<ZeroDowntimeRefusalModal onClose={vi.fn()} problems={undefined} />);
    expect(screen.queryByRole('dialog')).toBeNull();
  });
});
