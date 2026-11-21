import { Typography, theme } from 'antd';
import type { CSSProperties } from 'react';
import { useState } from 'react';

import black from '../assets/relist-chan-black.webp';
import silver from '../assets/relist-chan-silver.webp';
import './BootSplash.css';

/** One loop of the float and the glow, in ms (BootSplash.css). */
const LOOP_MS = 9600;

/** Relist-chan's looks, each with the light that suits her: gold for the silver hair, lavender for the black. */
const MASCOTS = [
  { src: silver, glow: 'rgba(214, 170, 92, 0.4)' },
  { src: black, glow: 'rgba(150, 128, 196, 0.36)' },
];

/** Picked once per page load, so the hand-over from RequireAuth to BootGate keeps the same one. */
const mascot = MASCOTS[Math.floor(Math.random() * MASCOTS.length)];

/**
 * Relist-chan, waiting with the visitor while the panel loads: the full-screen
 * splash `RequireAuth` shows before the sign-in status settles and `BootGate`
 * keeps over the app until its first data is in. `label` says what is still
 * loading, so an empty screen never reads as lost data.
 *
 * The two mount one after the other, so the loops start from the page clock
 * rather than from mount: the hand-over does not jump the float back.
 */
export function BootSplash({ label, leaving = false, fadeMs = 0 }: { label: string; leaving?: boolean; fadeMs?: number }) {
  const { token } = theme.useToken();
  const [phase] = useState(() => -(performance.now() % LOOP_MS));
  const style = {
    '--rp-boot-loop-offset': `${phase}ms`,
    '--rp-boot-fade': `${fadeMs}ms`,
    '--rp-boot-glow': mascot.glow,
    background: token.colorBgLayout,
    zIndex: token.zIndexPopupBase + 1000,
  } as CSSProperties;
  return (
    <div aria-label="Loading" className={leaving ? 'rp-boot rp-boot--leaving' : 'rp-boot'} role="status" style={style}>
      <div className="rp-boot__stage">
        <div aria-hidden className="rp-boot__glow" />
        <img alt="" className="rp-boot__mascot" draggable={false} height={224} src={mascot.src} width={224} />
      </div>
      <Typography.Text className="rp-boot__label" type="secondary">
        {label}
        <span aria-hidden className="rp-boot__dots">
          <span>.</span>
          <span>.</span>
          <span>.</span>
        </span>
      </Typography.Text>
    </div>
  );
}
