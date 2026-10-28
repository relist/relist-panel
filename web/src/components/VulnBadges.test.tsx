// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { describe, expect, it } from 'vitest';

import { VulnBadges } from './VulnBadges';

describe('VulnBadges', () => {
  it('renders nothing while scanning is off', () => {
    const { container } = render(<MemoryRouter><VulnBadges badge={null} to="/vulnerabilities" /></MemoryRouter>);
    expect(container.textContent).toBe('');
  });

  it('links the counts to the filtered page', () => {
    render(<MemoryRouter><VulnBadges badge={{ critical: 0, high: 2, fixable: 1 }} to="/vulnerabilities?tab=cves&projectId=4" /></MemoryRouter>);
    const link = screen.getByRole('link', { name: '0 Critical, 2 High, 1 fixable vulnerabilities' });
    expect(link.getAttribute('href')).toBe('/vulnerabilities?tab=cves&projectId=4');
    expect(screen.getByText('0 Critical')).toBeTruthy();
  });
});
