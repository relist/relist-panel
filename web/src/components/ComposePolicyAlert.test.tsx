// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { describe, expect, it, vi } from 'vitest';

import type { ComposePolicy } from '../data/api';
import { ComposePolicyAlert, policyDeployState } from './ComposePolicyAlert';

const violating: ComposePolicy = {
  approved: false,
  configHash: null,
  violations: [{ message: 'privileged is not allowed', service: 'web' }],
};

describe('ComposePolicyAlert', () => {
  it('lists the violations and says an approver must approve', () => {
    render(<App><ComposePolicyAlert approver={false} onApprove={vi.fn()} policy={violating} /></App>);
    expect(screen.getByText('This Compose needs approval')).toBeTruthy();
    expect(screen.getByText('web: privileged is not allowed')).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Approve/ })).toBeNull();
  });

  it('lets an approver approve', async () => {
    const onApprove = vi.fn().mockResolvedValue(undefined);
    render(<App><ComposePolicyAlert approver onApprove={onApprove} policy={{ ...violating, configHash: 'abc' }} /></App>);
    await userEvent.setup().click(screen.getByRole('button', { name: /Approve/ }));
    expect(onApprove).toHaveBeenCalled();
  });

  it('shows nothing for a config that passes, and says when it is approved', () => {
    const { container, rerender } = render(<App><ComposePolicyAlert approver={false} onApprove={vi.fn()} policy={{ approved: false, configHash: null, violations: [] }} /></App>);
    expect(container.textContent).toBe('');
    rerender(<App><ComposePolicyAlert approver={false} onApprove={vi.fn()} policy={{ ...violating, approved: true }} /></App>);
    expect(screen.getByText('An approver approved this config')).toBeTruthy();
  });
});

describe('policyDeployState', () => {
  it('waits for a non-approver, offers Approve & Deploy to an approver', () => {
    expect(policyDeployState(violating, false, false)).toBe('waiting');
    expect(policyDeployState({ ...violating, configHash: 'abc' }, true, false)).toBe('approve');
    expect(policyDeployState({ ...violating, approved: true }, false, false)).toBe('deploy');
    expect(policyDeployState(undefined, false, false)).toBe('deploy');
    // Unsaved edits are checked when they are saved and queued.
    expect(policyDeployState(violating, false, true)).toBe('deploy');
  });
});
