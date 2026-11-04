/** The steps a Traefik apply shows, in order (ADR-0030). */
export const APPLY_STEPS = ['Dry run', 'Write', 'Restart', 'Health check', 'Result'] as const;

const STEP: Record<string, number> = {
  checking: 3,
  'dry-run': 0,
  ok: 4,
  refused: 0,
  restarting: 2,
  'rollback-failed': 4,
  'rolled-back': 4,
  'rolling-back': 3,
  validating: 0,
  writing: 1,
};

const FAILED = new Set(['refused', 'rolling-back', 'rolled-back', 'rollback-failed']);

/** Which of APPLY_STEPS a run in this state is on. */
export function stepIndex(state: string): number {
  return STEP[state] ?? 0;
}

/** The antd Steps status for the current step. */
export function stepStatus(state: string): 'process' | 'finish' | 'error' {
  if (FAILED.has(state)) return 'error';
  return state === 'ok' ? 'finish' : 'process';
}

/** Whether a run in this state is over. */
export function isTerminal(state: string): boolean {
  return state === 'ok' || state === 'rolled-back' || state === 'rollback-failed' || state === 'refused';
}

/**
 * What to do when Traefik did not come back even with the previous files: the
 * panel is then reachable only over SSH, or Traefik is fixed by hand on the host.
 */
export function RECOVERY_TEXT(port: number, backup?: string): string {
  const failed = backup ? ` (${backup} is the one this run put back, and it already failed)` : '';
  return [
    'Traefik did not come back, even with the previous files. Every Domain and this panel stay down until it does.',
    '',
    'Reach the panel over SSH from your machine:',
    `  ssh -L ${port}:127.0.0.1:${port} <host>`,
    `  then open http://127.0.0.1:${port}/`,
    '',
    'Or fix it on the host. See why Traefik stops:',
    '  docker logs --tail 50 relist-traefik',
    `Put an older backup back${failed}:`,
    '  D=/var/lib/relist-panel/.traefik/static   # $RELIST_DATA_DIR/.traefik/static if you changed it',
    '  ls -r $D/history',
    '  cp $D/history/<backup>/traefik.override.yml $D/history/<backup>/traefik.yml $D/',
    '  docker restart relist-traefik',
  ].join('\n');
}

/** Review & Apply, Restore and Restart all restart Traefik. */
export const RESTART_DROP = 'Every Domain and this panel drop for a few seconds while Traefik restarts';
