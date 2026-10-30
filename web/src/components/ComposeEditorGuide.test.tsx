// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ComposeEditorGuide } from './ComposeEditorGuide';
import { ZERO_DOWNTIME_REQUIREMENTS } from './ZeroDowntimeRequirements';

const todaysBullets = [
  'Containers should not write into ./src — use a named volume.',
  'To share a volume between Composes, declare it external: true.',
  "Relative bind mounts such as ./data live in this Compose's directory and are deleted with the Compose.",
];

describe('ComposeEditorGuide', () => {
  it('is today’s three bullets when no Service has the switch on', () => {
    const { container, unmount } = render(<ComposeEditorGuide />);
    const items = [...container.querySelectorAll('li')].map((li) => li.textContent);
    expect(items).toEqual(todaysBullets);
    const bare = container.innerHTML;
    unmount();
    const off = render(<ComposeEditorGuide zeroDowntime={[{ service: 'web', enabled: false, reasons: ['has no healthcheck'] }]} />);
    expect(off.container.innerHTML).toBe(bare);
  });

  it('lists the requirements and a ready Service', () => {
    render(<ComposeEditorGuide zeroDowntime={[{ service: 'web', enabled: true, reasons: [] }]} />);
    expect(screen.getByText('Zero-downtime deploy')).toBeTruthy();
    for (const line of ZERO_DOWNTIME_REQUIREMENTS) {
      expect(screen.getByText(line)).toBeTruthy();
    }
    expect(screen.getByText('web — ready')).toBeTruthy();
  });

  it('shows a Service that is not ready as a warning, with no Alert', () => {
    const { container } = render(
      <ComposeEditorGuide
        zeroDowntime={[
          { service: 'api', enabled: true, reasons: ['has no healthcheck', "publishes host port 8080; two Containers can't bind it"] },
        ]}
      />,
    );
    const line = screen.getByText("api — has no healthcheck; publishes host port 8080; two Containers can't bind it");
    expect(line.closest('.ant-typography-warning')).toBeTruthy();
    expect(container.querySelector('.ant-alert')).toBeNull();
  });
});
