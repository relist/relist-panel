// @vitest-environment jsdom
import '../test/dom-setup';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { describe, expect, it, vi } from 'vitest';
import { PanelVersionFooter } from './PanelVersionFooter';

describe('PanelVersionFooter', () => {
  it('shows the running version as a link to Panel update', () => {
    const onNavigate = vi.fn();
    render(<MemoryRouter><PanelVersionFooter onNavigate={onNavigate} version="0.1.0-rc.1" /></MemoryRouter>);
    const link = screen.getByRole('link', { name: 'Relist Panel 0.1.0-rc.1' });
    expect(link.getAttribute('href')).toBe('/panel-update');
    fireEvent.click(link);
    expect(onNavigate).toHaveBeenCalled();
  });

  it('shows a development build as dev', () => {
    render(<MemoryRouter><PanelVersionFooter version="dev" /></MemoryRouter>);
    expect(screen.getByRole('link', { name: 'Relist Panel dev' })).toBeTruthy();
  });

  it('renders nothing before the version is known', () => {
    const { container } = render(<MemoryRouter><PanelVersionFooter version={undefined} /></MemoryRouter>);
    expect(container.textContent).toBe('');
  });
});
