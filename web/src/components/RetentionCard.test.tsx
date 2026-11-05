// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen } from '@testing-library/react';
import { App } from 'antd';
import { describe, expect, it, vi } from 'vitest';

import { RetentionCard } from './RetentionCard';

describe('RetentionCard', () => {
  it('caps the days at max', () => {
    render(
      <App>
        <RetentionCard keepForever={false} max={36500} onSave={vi.fn()} onSaved={vi.fn()} retentionDays={90} />
      </App>,
    );
    expect(screen.getByLabelText('Days to keep').getAttribute('aria-valuemax')).toBe('36500');
  });
});
