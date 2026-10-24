// @vitest-environment jsdom
import '../test/dom-setup';
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';

import { ComposeEditorGuide } from './ComposeEditorGuide';

afterEach(cleanup);

describe('ComposeEditorGuide', () => {
  it('lists what to avoid in the Compose YAML', () => {
    render(<ComposeEditorGuide />);
    expect(screen.getAllByRole('listitem').map((item) => item.textContent)).toEqual([
      'Containers should not write into ./src — use a named volume.',
      'To share a volume between Composes, declare it external: true.',
      "Relative bind mounts such as ./data live in this Compose's directory and are deleted with the Compose.",
    ]);
  });
});
