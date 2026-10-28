import { theme } from 'antd';
import type { ThemeConfig } from 'antd';
import type { editor } from 'monaco-editor';
import { createContext } from 'react';

/**
 * The redesign keeps Ant Design's default seed palette (DESIGN.md is the
 * contract) and layers a thin set of *semantic* rules on top:
 *
 *  - one status vocabulary for Compose lifecycle (`COMPOSE_STATE_META`)
 *  - one layout grammar (56px bars, 24px gutters, token-driven surfaces)
 *  - component-token nudges so antd's defaults match that grammar
 *
 * No colour is ever hard-coded in a page: everything comes from
 * `theme.useToken()` or from the overrides below.
 */

export const FONT_STACK =
  "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, 'Noto Sans', sans-serif";

export const CODE_FONT_STACK = "'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, Courier, monospace";

export const LAYOUT = {
  siderWidth: 248,
  barHeight: 56,
  contentMaxWidth: 1280,
  gutter: 24,
  gutterSmall: 16,
  /**
   * The Compose editor sizes itself to its content (Monaco still needs a bounded
   * box, so it is given the content height), between this floor — short files
   * stay a usable box — and the cap below, past which it scrolls inside.
   */
  editorAutoMinHeight: 320,
  editorAutoMaxHeight: '80vh',
  /**
   * The Terminal tab's xterm box (GUIDELINES §6, §4.6): a bounded box because
   * a terminal *is* the feature, tall enough for `vim` and `top` without
   * giving the whole viewport to it — the sheet around it still scrolls as one
   * document. `clamp` keeps it usable on a short laptop and on a tall monitor.
   */
  terminalHeight: 'clamp(420px, 65vh, 800px)',
} as const;

export type ThemeMode = 'dark' | 'light';

/**
 * The mode is React state in `main.tsx`. Monaco themes itself, so it cannot read
 * antd tokens off the React tree — anything with its own theming system reads
 * the mode from here instead.
 */
export const ThemeModeContext = createContext<ThemeMode>('light');

/** Colour vocabulary for a Compose's derived lifecycle state. */
export const COMPOSE_STATE_META = {
  running: { badge: 'success', hint: 'Every container is up', label: 'Running' },
  partial: { badge: 'warning', hint: 'Some containers are down — check the service list', label: 'Partly running' },
  stopped: { badge: 'default', hint: 'Previously deployed, currently down', label: 'Stopped' },
  'not-deployed': { badge: 'default', hint: 'Saved but never deployed to the host', label: 'Not deployed' },
} as const satisfies Record<string, { badge: 'default' | 'success' | 'warning'; hint: string; label: string }>;

export const SERVICE_STATE_META = {
  running: { badge: 'success', label: 'running' },
  exited: { badge: 'error', label: 'exited' },
  restarting: { badge: 'warning', label: 'restarting' },
  created: { badge: 'default', label: 'created' },
} as const;

const seed = (mode: ThemeMode): ThemeConfig => ({
  algorithm: mode === 'dark' ? theme.darkAlgorithm : theme.defaultAlgorithm,
  token: {
    colorPrimary: '#1677FF',
    colorInfo: '#1677FF',
    borderRadius: 6,
    borderRadiusLG: 8,
    fontFamily: FONT_STACK,
    fontSize: 14,
    ...(mode === 'light' ? { colorBgLayout: '#F5F5F5' } : {}),
  },
});

/** Resolved tokens for a mode, without mounting a ConfigProvider. */
export function designTokens(mode: ThemeMode) {
  return theme.getDesignToken(seed(mode));
}

export const MONACO_THEME: Record<ThemeMode, string> = {
  dark: 'relist-dark',
  light: 'relist-light',
};

/**
 * Monaco only parses hex colours, while antd's text and fill ramps are `rgba()`.
 * Same colour, written the way the editor's theme parser accepts it — alpha
 * survives as the 8-digit hex form.
 */
export function toMonacoColor(value: string): string {
  const parse = (color: string): [number, number, number, number] => {
    if (color.startsWith('#')) {
      const hex = color.slice(1);
      const full = hex.length <= 4 ? [...hex].map((digit) => digit + digit).join('') : hex;
      const value = Number.parseInt(full, 16);
      return [(value >> 16) & 255, (value >> 8) & 255, value & 255, full.length === 8 ? ((value & 255) / 255) : 1];
    }
    const [r, g, b, a = 1] = (color.match(/[\d.]+/g) ?? ['0', '0', '0', '1']).map(Number);
    return [r, g, b, a];
  };

  const [r, g, b, a] = parse(value);
  const hex = [r, g, b].map((channel) => Math.round(channel).toString(16).padStart(2, '0')).join('');
  return a < 1 ? `#${hex}${Math.round(a * 255).toString(16).padStart(2, '0')}` : `#${hex}`;
}

/**
 * Monaco has its own theme system, so the editor is the one surface that has to
 * be told about the mode twice. Its *surfaces* come from the same design tokens
 * as the rest of the panel (background, text ramp, primary ramp); the *syntax*
 * palette stays with Monaco's own light/dark themes through `inherit`, because
 * antd has no opinion about what a YAML string should look like.
 */
export function monacoThemeData(mode: ThemeMode): editor.IStandaloneThemeData {
  const token = designTokens(mode);
  const colors: Record<string, string> = {
    'editor.background': token.colorBgContainer,
    'editor.foreground': token.colorText,
    'editorCursor.foreground': token.colorPrimary,
    'editorGutter.background': token.colorBgContainer,
    'editorHoverWidget.background': token.colorBgElevated,
    'editorIndentGuide.activeBackground1': token.colorBorder,
    'editorIndentGuide.background1': token.colorBorderSecondary,
    'editorLineNumber.activeForeground': token.colorTextSecondary,
    'editorLineNumber.foreground': token.colorTextQuaternary,
    'editorSuggestWidget.background': token.colorBgElevated,
    'editorSuggestWidget.selectedBackground': token.colorFillQuaternary,
    'editorWidget.background': token.colorBgElevated,
    'editorWidget.border': token.colorBorderSecondary,
    'editor.lineHighlightBackground': token.colorFillQuaternary,
    'editor.selectionBackground': token.colorPrimaryBorder,
    'editor.selectionHighlightBackground': token.colorPrimaryBg,
    'editorBracketMatch.background': token.colorFillQuaternary,
    'editorBracketMatch.border': token.colorBorder,
    'scrollbarSlider.activeBackground': token.colorTextQuaternary,
    'scrollbarSlider.background': token.colorFillSecondary,
    'scrollbarSlider.hoverBackground': token.colorFill,
  };

  return {
    base: mode === 'dark' ? 'vs-dark' : 'vs',
    colors: Object.fromEntries(Object.entries(colors).map(([key, value]) => [key, toMonacoColor(value)])),
    inherit: true,
    rules: [],
  };
}

export function buildTheme(mode: ThemeMode): ThemeConfig {
  const tokens = designTokens(mode);
  return {
    ...seed(mode),
    components: {
      Layout: {
        bodyBg: tokens.colorBgLayout,
        headerBg: tokens.colorBgContainer,
        headerHeight: LAYOUT.barHeight,
        headerPadding: `0 ${LAYOUT.gutter}px`,
        siderBg: tokens.colorBgContainer,
      },
      Menu: {
        groupTitleColor: tokens.colorTextTertiary,
        itemBorderRadius: 6,
        itemHeight: 34,
        itemMarginInline: 8,
        itemMarginBlock: 2,
        subMenuItemBg: 'transparent',
      },
      Table: {
        cellPaddingBlock: 12,
        headerBg: tokens.colorFillQuaternary,
        headerSplitColor: 'transparent',
        rowHoverBg: tokens.colorFillQuaternary,
      },
      Card: {
        headerFontSize: 14,
        headerFontSizeSM: 14,
      },
      Descriptions: {
        labelBg: tokens.colorFillQuaternary,
      },
      Statistic: {
        titleFontSize: 13,
      },
      Tag: {
        defaultBg: tokens.colorFillQuaternary,
      },
    },
  };
}
