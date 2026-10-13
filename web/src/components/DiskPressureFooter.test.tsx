// @vitest-environment jsdom
import '../test/dom-setup';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { describe, expect, it } from 'vitest';
import { DiskPressureFooter } from './DiskPressureFooter';

describe('DiskPressureFooter', () => {
  it('shows percentage, free bytes, affected filesystem and Monitoring for a warning', () => {
    render(<MemoryRouter><DiskPressureFooter pressure={{ kind: 'warning', percent: 80, freeBytes: 2048, at: 100_000 }} /></MemoryRouter>);
    expect(screen.getByRole('contentinfo', { name: 'Docker host storage' })).toBeTruthy();
    expect(screen.getByText('Panel data disk is nearly full')).toBeTruthy();
    expect(screen.getByText('80% used · 2.0 kB free')).toBeTruthy();
    expect(screen.getByText('Filesystem containing Panel and SQLite data.')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Open Monitoring' }).getAttribute('href')).toBe('/monitoring');
  });

  it('keeps the last warning visible with an explicit stale label', () => {
    render(<MemoryRouter><DiskPressureFooter pressure={{ kind: 'stale', warning: true, percent: 90, freeBytes: 1024, at: 100_000, reason: 'offline' }} /></MemoryRouter>);
    expect(screen.getByText('Disk usage stale')).toBeTruthy();
    expect(screen.getByText('Last reading: 90% used · 1.0 kB free')).toBeTruthy();
    expect(screen.getByText('Current usage is unknown.')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Open Monitoring' })).toBeTruthy();
  });

  it('represents an unavailable reading without a zero percentage', () => {
    render(<MemoryRouter><DiskPressureFooter pressure={{ kind: 'unavailable', reason: 'statfs denied '.repeat(100) }} /></MemoryRouter>);
    expect(screen.getByText('Disk usage unavailable')).toBeTruthy();
    expect(screen.queryByText(/0% used/)).toBeNull();
    expect(screen.getByRole('link', { name: 'Open Monitoring' })).toBeTruthy();
  });

  it('removes the pressure warning after healthy recovery', () => {
    const footer = (kind: 'warning' | 'healthy') => <MemoryRouter><DiskPressureFooter pressure={{ kind, percent: kind === 'healthy' ? 79.9 : 80, freeBytes: 200, at: 100_000 }} /></MemoryRouter>;
    const result = render(footer('warning'));
    result.rerender(footer('healthy'));
    expect(screen.queryByText('Panel data disk is nearly full')).toBeNull();
    expect(screen.getByText('Docker host')).toBeTruthy();
  });
});
