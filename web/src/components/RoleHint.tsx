import { Tooltip } from 'antd';
import type { ReactNode } from 'react';

import type { Level } from '../data/useCan';

/** What a disabled action says it needs (GUIDELINES §9: say why, do not hide). */
export function needsRole(level: Level | 'project editor' | 'admin'): string {
  if (level === 'project editor') return 'Needs the Editor role on the Project';
  if (level === 'admin') return 'Only an admin can do this';
  return `Needs the ${level.charAt(0).toUpperCase()}${level.slice(1)} role`;
}

/**
 * Wraps a control the User's role leaves disabled in a tooltip that says so.
 * The span lets the tooltip fire over a disabled button. When allowed, the
 * child renders as it is.
 */
export function RoleHint({ allowed, children, level }: { allowed: boolean; children: ReactNode; level: Parameters<typeof needsRole>[0] }) {
  if (allowed) return <>{children}</>;
  return (
    <Tooltip title={needsRole(level)}>
      <span style={{ display: 'inline-flex' }}>{children}</span>
    </Tooltip>
  );
}
