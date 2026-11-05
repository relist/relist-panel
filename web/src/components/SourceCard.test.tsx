// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { SourceCard } from './SourceCard';
import type { ComposeSource } from '../data/types';

const source: ComposeSource = {
  kind: 'git',
  repoUrl: 'git@github.com:acme/app.git',
  branch: 'main',
  submodules: false,
  sparsePaths: [],
  sshKey: null,
  headSha: '',
  lastTest: null,
  zip: null,
};

const noop = () => {};

describe('SourceCard', () => {
  it('offers build snippets when the viewer can write the file', () => {
    render(<SourceCard draft="services: {}" lastBuilt={undefined} onConfigure={noop} onShowSnippets={noop} onUploadNew={noop} onViewDeployments={noop} source={source} />);
    expect(screen.getByRole('button', { name: /Build args/ })).toBeTruthy();
  });

  it('offers no snippets to insert when only an admin writes the file', () => {
    render(<SourceCard draft="services: {}" lastBuilt={undefined} onConfigure={noop} onUploadNew={noop} onViewDeployments={noop} source={source} />);
    expect(screen.getByText(/No Service has build: yet/)).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Build args/ })).toBeNull();
  });
});
