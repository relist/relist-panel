/**
 * The two outbound links a review surface offers for a scene (ticket 11).
 *
 * Both used to live inside `BoardPage`'s frame chrome; the task flow page needs
 * the same two, and a second copy of the URL rules would be exactly the second
 * source of truth this mock avoids — a scene's forced state has one spelling,
 * and `?ui=`/`?after=` is it.
 *
 *  - `openLiveUrl` — the interactive mock at the scene's exact forced state:
 *    `?ui=`/`?after=` from the scene, `?theme=dark` while the calling surface is
 *    dark, and the scene's hash. Built from a fresh search so the caller's own
 *    chrome state never leaks into a live page.
 *  - `boardSceneUrl` — the board's deep link. The board reads `?scene=` from
 *    the *search string*, not the hash (`BoardPage`), so the id goes there and
 *    the hash stays `#/design/board` — `?scene=compose-editor-running#/design/board`.
 */

import { paths } from '../app/router';

import { serializeUiValue, toUiValue } from './adapter';
import type { Scene } from './types';

/** The live mock at the scene's exact state. `base` is for tests. */
export function openLiveUrl(scene: Scene, dark: boolean, base = window.location.href): string {
  const url = new URL(base);
  url.search = '';
  if (dark) url.searchParams.set('theme', 'dark');
  for (const [key, raw] of Object.entries(scene.ui ?? {})) {
    url.searchParams.append('ui', `${key}=${serializeUiValue(toUiValue(raw))}`);
  }
  if (scene.after !== undefined) url.searchParams.set('after', scene.after);
  url.hash = scene.hash;
  return url.toString();
}

/** The board centered on one frame. */
export function boardSceneUrl(sceneId: string, base = window.location.href): string {
  const url = new URL(base);
  url.search = '';
  url.searchParams.set('scene', sceneId);
  url.hash = paths.board;
  return url.toString();
}
