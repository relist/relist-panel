import { describe, expect, it } from 'vitest';

import { CODE_FONT_STACK, COMPOSE_STATE_META, LAYOUT, toMonacoColor } from './theme';

describe('LAYOUT', () => {
  // The layout grammar (GUIDELINES §6) in one table; screen tickets copy
  // markup across against these keys, so a rename here is a breaking change.
  it('carries the design layout keys', () => {
    expect(LAYOUT).toEqual({
      siderWidth: 248,
      barHeight: 56,
      contentMaxWidth: 1280,
      gutter: 24,
      gutterSmall: 16,
      editorMinHeight: 'clamp(640px, 80vh, 1000px)',
    });
  });
});

describe('toMonacoColor', () => {
  it('passes hex colours through', () => {
    expect(toMonacoColor('#1677FF')).toBe('#1677ff');
  });

  it('expands short hex', () => {
    expect(toMonacoColor('#fff')).toBe('#ffffff');
  });

  it('writes rgba() alpha as an 8-digit hex', () => {
    expect(toMonacoColor('rgba(0, 0, 0, 0.03)')).toBe('#00000008');
    expect(toMonacoColor('rgba(255, 255, 255, 0.5)')).toBe('#ffffff80');
  });

  it('keeps fully opaque rgb() at six digits', () => {
    expect(toMonacoColor('rgb(22, 119, 255)')).toBe('#1677ff');
  });
});

describe('status vocabulary', () => {
  it('labels every derived Compose state', () => {
    expect(Object.keys(COMPOSE_STATE_META).sort()).toEqual(['not-deployed', 'partial', 'running', 'stopped']);
    expect(COMPOSE_STATE_META['not-deployed'].label).toBe('Not deployed');
    expect(COMPOSE_STATE_META.partial.label).toBe('Partly running');
  });

  it('keeps the mono font stack the prototype ships', () => {
    expect(CODE_FONT_STACK).toContain('SFMono-Regular');
  });
});
