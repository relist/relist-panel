import { useEffect, useState } from 'react';
import { usePanel } from '../data/store';
import { sceneId } from './scene';

/**
 * Publishes `data-scene-ready` on `<html>`, carrying the `?scene=` id back.
 *
 * The capture pipeline's own assertion (spec: *The capture pipeline*, step 3):
 * a frame may only be photographed once the app says it is the scene that was
 * asked for, so a capture can never silently be of the previous route, of a
 * half-loaded screen, or of the scene before it. Readiness is deliberately
 * *withdrawn* while the store is pending — several screens load from an effect,
 * so the signal has to go down before it goes up, or a photograph would catch
 * the spinner.
 *
 * Three conditions, and Monaco-idle is not one of them: that belongs to the
 * pipeline ticket, which extends this signal rather than replacing it.
 *
 * Rendered inside `PanelProvider`, because `pending` is store state. Capture runs
 * a production build where effects run once; under `pnpm dev` `StrictMode`
 * double-invokes them and this attribute flaps, which is one more reason the
 * pipeline never drives the dev server.
 */
export function SceneReadiness(): null {
  const { pending } = usePanel();
  const [fontsReady, setFontsReady] = useState(false);
  const scene = sceneId();
  const ready = scene !== null && fontsReady && Object.keys(pending).length === 0;

  useEffect(() => {
    let active = true;
    const fonts = document.fonts;
    if (!fonts) {
      setFontsReady(true);
      return undefined;
    }
    void fonts.ready.then(() => {
      if (active) setFontsReady(true);
    });
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    const root = document.documentElement;
    if (ready && scene !== null) {
      root.setAttribute('data-scene-ready', scene);
    } else {
      root.removeAttribute('data-scene-ready');
    }
  }, [ready, scene]);

  return null;
}
