// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Compose, ComposeVariable, Environment } from '../data/types';
import { ComposeVariablesTab } from './ComposeVariablesTab';

const panel = vi.hoisted(() => ({
  deleteComposeVariable: vi.fn(),
  isPending: vi.fn(() => false),
  loadBindings: vi.fn(),
  loadDomains: vi.fn(),
  loadVariableSets: vi.fn(),
  saveComposeVariable: vi.fn(),
}));

vi.mock('../data/store', () => ({ usePanel: () => panel }));

const compose: Compose = {
  createdAt: '2026-10-09T09:00:00Z',
  domainsChangedAt: null,
  envSlug: '',
  environmentId: 1,
  id: 7,
  lastDeployedAt: '2026-10-09T11:00:00Z',
  name: 'web',
  projectSlug: '',
  status: { running: 0, total: 0 },
  updatedAt: '2026-10-09T09:00:00Z',
  variablesChangedAt: null,
  yamlContent: '',
};

const environment = (variables: Environment['variables']): Environment => ({
  composes: [],
  createdAt: '',
  id: 1,
  name: 'production',
  projectId: 1,
  variables,
});

const envVar = (key: string, value: string, secret = false) => ({ environmentId: 1, id: 1, key, secret, updatedAt: '', value });
const composeVar = (id: number, key: string, value: string, secret = false): ComposeVariable => ({ composeId: 7, id, key, secret, value });

function renderTab(props: Partial<React.ComponentProps<typeof ComposeVariablesTab>> = {}) {
  const onChanged = vi.fn();
  const view = render(
    <App>
      <ComposeVariablesTab
        compose={compose}
        deploying={false}
        environment={environment([])}
        onChanged={onChanged}
        onDeploy={vi.fn()}
        onError={vi.fn()}
        variables={[]}
        {...props}
      />
    </App>,
  );
  return { ...view, onChanged };
}

describe('ComposeVariablesTab', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    panel.isPending.mockReturnValue(false);
    panel.loadVariableSets.mockResolvedValue([]);
    panel.loadBindings.mockResolvedValue([]);
    panel.loadDomains.mockResolvedValue({ domains: [], services: [] });
  });

  it('shows only the empty state and one add button when there are no Compose variables', () => {
    renderTab();
    expect(screen.getByText('No Compose variables yet')).toBeTruthy();
    expect(screen.getAllByRole('button', { name: /New Variable|Add a Variable/ })).toHaveLength(1);
    expect(screen.getByRole('button', { name: /Add a Variable/ })).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Paste/ })).toBeNull();
  });

  it('shows the override hint with the Environment Variable value, masking a secret one', () => {
    renderTab({
      environment: environment([envVar('LOG_LEVEL', 'info'), envVar('API_TOKEN', 'hunter2', true)]),
      variables: [composeVar(1, 'LOG_LEVEL', 'debug'), composeVar(2, 'API_TOKEN', 'x'), composeVar(3, 'ONLY_HERE', '1')],
    });
    expect(screen.getByText('overrides production: info')).toBeTruthy();
    expect(screen.getByText('overrides production: ••••')).toBeTruthy();
    expect(screen.queryByText(/hunter2/)).toBeNull();
    expect(screen.getAllByText(/overrides production/)).toHaveLength(2);
  });

  it('adds and drops the hint as the matching Environment Variable comes and goes', () => {
    const variables = [composeVar(1, 'LOG_LEVEL', 'debug')];
    const { rerender } = renderTab({ variables });
    expect(screen.queryByText(/^overrides /)).toBeNull();

    const tab = (env: Environment) => (
      <App>
        <ComposeVariablesTab compose={compose} deploying={false} environment={env} onChanged={vi.fn()} onDeploy={vi.fn()} onError={vi.fn()} variables={variables} />
      </App>
    );
    rerender(tab(environment([envVar('LOG_LEVEL', 'info')])));
    expect(screen.getByText('overrides production: info')).toBeTruthy();
    rerender(tab(environment([])));
    expect(screen.queryByText(/^overrides /)).toBeNull();
  });

  it('masks a secret Compose variable until revealed', async () => {
    const user = userEvent.setup();
    renderTab({ variables: [composeVar(1, 'DB_PASSWORD', 'p4ssw0rd', true)] });
    expect(screen.queryByText('p4ssw0rd')).toBeNull();
    expect(screen.getByText('secret')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Reveal value' }));
    expect(screen.getByText('p4ssw0rd')).toBeTruthy();
  });

  it('saves a new variable through the store and refetches', async () => {
    panel.saveComposeVariable.mockResolvedValue(undefined);
    const user = userEvent.setup();
    const { onChanged } = renderTab();

    await user.click(screen.getByRole('button', { name: /Add a Variable/ }));
    const dialog = await screen.findByRole('dialog');
    await user.type(within(dialog).getByLabelText('Key'), 'API_TOKEN');
    await user.type(within(dialog).getByLabelText('Value'), 'abc');
    await user.click(within(dialog).getByRole('button', { name: 'Add Variable' }));

    await waitFor(() => expect(panel.saveComposeVariable).toHaveBeenCalledWith(7, { key: 'API_TOKEN', secret: true, value: 'abc' }, undefined));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
  });

  it('keeps a failed save in the modal', async () => {
    panel.saveComposeVariable.mockRejectedValue(new Error('variable key already exists'));
    const user = userEvent.setup();
    const { onChanged } = renderTab({ variables: [composeVar(1, 'LOG_LEVEL', 'debug')] });

    await user.click(screen.getByRole('button', { name: 'Edit LOG_LEVEL' }));
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByRole('button', { name: 'Save changes' }));

    expect(await within(dialog).findByText('variable key already exists')).toBeTruthy();
    expect(onChanged).not.toHaveBeenCalled();
  });

  it('deletes through the store after confirming', async () => {
    panel.deleteComposeVariable.mockResolvedValue(undefined);
    const user = userEvent.setup();
    const { onChanged } = renderTab({ variables: [composeVar(4, 'LOG_LEVEL', 'debug')] });

    await user.click(screen.getByRole('button', { name: 'Delete LOG_LEVEL' }));
    await user.click(await screen.findByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(panel.deleteComposeVariable).toHaveBeenCalledWith(7, 4));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
  });

  it('shows the redeploy banner only when a variable changed since the last deploy', () => {
    const pending = renderTab({ compose: { ...compose, variablesChangedAt: '2026-10-09T12:00:00Z' } });
    expect(screen.getByText('Variable changes apply on the next deploy.')).toBeTruthy();
    pending.unmount();

    renderTab({ compose: { ...compose, variablesChangedAt: '2026-10-09T10:00:00Z' } });
    expect(screen.queryByText(/apply on the next deploy/)).toBeNull();
  });
});
