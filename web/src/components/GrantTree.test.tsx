// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';

import type { GrantInput } from './GrantTree';
import { GrantTree } from './GrantTree';

const projects = [{ id: 1, name: 'shop', environments: [{ id: 11, name: 'production' }, { id: 12, name: 'staging' }] }];

function Harness({ initial, onChange }: { initial: GrantInput[]; onChange: (value: GrantInput[]) => void }) {
  const [value, setValue] = useState(initial);
  return (
    <App>
      <GrantTree onChange={(next) => { setValue(next); onChange(next); }} projects={projects} value={value} />
    </App>
  );
}

async function pick(label: string, option: string) {
  const user = userEvent.setup();
  await user.click(screen.getByRole('combobox', { name: label }));
  const listbox = await screen.findAllByTitle(option);
  await user.click(listbox[listbox.length - 1]);
}

describe('GrantTree', () => {
  it('sets an Environment to No access', async () => {
    const onChange = vi.fn();
    render(<Harness initial={[{ projectId: 1, environmentId: null, role: 'editor' }]} onChange={onChange} />);
    await pick('Role on shop / production', 'No access');
    expect(onChange).toHaveBeenLastCalledWith([
      { projectId: 1, environmentId: null, role: 'editor' },
      { projectId: 1, environmentId: 11, role: 'none' },
    ]);
  });

  it('drops an Environment Grant on Same as Project, and a Project Grant on No grant', async () => {
    const onChange = vi.fn();
    render(<Harness initial={[{ projectId: 1, environmentId: null, role: 'viewer' }, { projectId: 1, environmentId: 12, role: 'editor' }]} onChange={onChange} />);
    await pick('Role on shop / staging', 'Same as Project');
    expect(onChange).toHaveBeenLastCalledWith([{ projectId: 1, environmentId: null, role: 'viewer' }]);
    await pick('Role on shop', 'No grant');
    expect(onChange).toHaveBeenLastCalledWith([]);
  });

  it('offers no No access on a Project', async () => {
    render(<Harness initial={[]} onChange={vi.fn()} />);
    const user = userEvent.setup();
    await user.click(screen.getByRole('combobox', { name: 'Role on shop' }));
    const listbox = await screen.findByRole('listbox');
    expect(within(listbox.parentElement!).queryByText('No access')).toBeNull();
    expect(screen.getAllByTitle('Editor').length).toBeGreaterThan(0);
  });
});
