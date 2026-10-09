/**
 * Shared setup for component tests. Import it first in a test file that opts
 * into jsdom with `// @vitest-environment jsdom`; the other tests keep the
 * default environment.
 */
import { cleanup } from '@testing-library/react';
import { afterEach } from 'vitest';

// antd's responsive helpers call matchMedia, which jsdom does not implement.
Object.defineProperty(window, 'matchMedia', {
  configurable: true,
  value: (query: string): MediaQueryList => ({
    addEventListener: () => undefined,
    addListener: () => undefined,
    dispatchEvent: () => false,
    matches: false,
    media: query,
    onchange: null,
    removeEventListener: () => undefined,
    removeListener: () => undefined,
  }),
  writable: true,
});

// rc-resize-observer (Table, Select, Modal) needs ResizeObserver.
class ResizeObserverStub implements ResizeObserver {
  disconnect(): void {}
  observe(): void {}
  unobserve(): void {}
}
globalThis.ResizeObserver = ResizeObserverStub;

// jsdom's getComputedStyle throws "not implemented" when given a pseudo
// element, which rc-table uses to measure the scrollbar. Ignore the pseudo.
const getComputedStyleOrig = window.getComputedStyle.bind(window);
window.getComputedStyle = (element: Element) => getComputedStyleOrig(element);

afterEach(() => {
  cleanup();
});
