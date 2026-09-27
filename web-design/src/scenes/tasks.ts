/**
 * The task flows — the journeys a reviewer actually walks, written down as
 * ordered steps (ticket 11).
 *
 * The board is an inventory: every scene at its real size. Walking a journey on
 * it means chasing dashed lines across a canvas of full-size frames, which is
 * both unreadable and expensive. A *task* is that journey written down — and
 * written down *against the registry*, so it cannot drift:
 *
 *  - every step is a real scene id;
 *  - a hop between two steps is either a declared `SceneLink` (the default —
 *    and then the connector's label is the link's own label, never re-typed
 *    here) or an explicit `via: { kind: 'nav', label }` naming the control a
 *    human uses ("Sign out"), which no registry link can express;
 *  - `validateTasks` is the whole contract: it fails a step that names no
 *    scene, a hop that declares neither, and a nav hop that claims to be
 *    hand-navigated while the registry already links the pair.
 *
 * `scripts/flow.test.mjs` runs `validateTasks(TASKS, SCENES)` against the
 * assembled registry, so a link rename that breaks a task is a red test and
 * not a quietly wrong diagram. The page renders any problem loudly too — a
 * drifted task should be visible, and only ever in a screenshot.
 *
 * Data-only (type-only import): `node --test` loads this module.
 */

import type { Scene, SceneLink } from './types';

/** How the walker arrived at a step from the one before it. */
export type TaskStepVia =
  /** A declared registry link — the default; the label is the link's own. */
  | { kind: 'link' }
  /** Menu or route navigation: a real control, named the way the UI names it. */
  | { kind: 'nav'; label: string };

export type TaskStep = {
  sceneId: string;
  /** One line of reviewer cue, rendered under the card. */
  note?: string;
  /** Required when the hop is not a declared link; step 1 never carries one. */
  via?: TaskStepVia;
};

export type Task = {
  /** Kebab-case, unique; the section anchor is `#/design/flow#<id>`. */
  id: string;
  /** The journey's name — an action, like a commit subject. */
  label: string;
  /** One sentence: the question this task answers. */
  summary: string;
  /** 3–8 steps, in walking order. */
  steps: TaskStep[];
};

/**
 * The six journeys. Chosen to cover the app's entrances: first run, creating a
 * Project, the Project tabs, a broken Compose, a Volume handle, signing out.
 * Adding one is an editorial act — but a cheap one, because `validateTasks`
 * will not let it name a scene or a hop that does not exist.
 */
export const TASKS: readonly Task[] = [
  {
    id: 'first-run',
    label: 'First run: set up, then sign in',
    summary: 'The two auth screens and the landing an operator arrives at.',
    steps: [
      { sceneId: 'auth-setup', note: 'No admin exists yet: step 1 of 2.' },
      { sceneId: 'auth-setup-ready', via: { kind: 'nav', label: 'Create the admin' }, note: 'Same route, second step — the toast is the app\'s own.' },
      { sceneId: 'auth-signin', via: { kind: 'nav', label: 'Go to sign in' }, note: 'There is no admin to sign in as before this succeeds.' },
      { sceneId: 'overview', via: { kind: 'nav', label: 'Sign in' }, note: 'The session starts on the Overview.' },
    ],
  },
  {
    id: 'create-project',
    label: 'Create a Project',
    summary: 'The two-field modal, the invalid submit, and where a created Project lands.',
    steps: [
      { sceneId: 'projects-cards', note: 'Start: the Projects grid.' },
      { sceneId: 'projects-new-modal', note: 'Name and description only — no Environment or Compose decisions.' },
      { sceneId: 'projects-new-modal-errors', note: 'The real handler\'s result: errors under the name field, nothing typed lost.' },
      { sceneId: 'project-composes', via: { kind: 'nav', label: 'Fix and create — open my-saas' }, note: 'The Project exists; the drill-down is where the next decisions live.' },
    ],
  },
  {
    id: 'project-tabs',
    label: 'Drill into a Project',
    summary: 'The tab bar is the Project\'s own flow; the danger zone sits at its end.',
    steps: [
      { sceneId: 'projects-cards', note: 'Start: pick my-saas out of the grid.' },
      { sceneId: 'project-composes', via: { kind: 'nav', label: 'Open my-saas / production' } },
      { sceneId: 'project-variables', note: 'A tab, not a route change: the Environment switcher keeps the context.' },
      { sceneId: 'project-settings' },
      { sceneId: 'project-delete-modal', note: 'The tab bar ends where the destructive confirm begins.' },
    ],
  },
  {
    id: 'diagnose-partial',
    label: 'Diagnose a partial Compose',
    summary: 'From the half-running row to the fatal log line and the chart that stops with it.',
    steps: [
      { sceneId: 'project-composes', note: 'worker is 1 of 2 — the row says so without opening anything.' },
      { sceneId: 'compose-editor-partial', via: { kind: 'nav', label: 'Open worker' } },
      { sceneId: 'compose-logs-fatal', note: 'The drawer beside the editor; worker-cron\'s last lines are the story.' },
      { sceneId: 'compose-monitor-partial', via: { kind: 'nav', label: 'Monitor tab' }, note: 'The exited service\'s line stops; it does not flatline.' },
    ],
  },
  {
    id: 'volume-handle',
    label: 'Open a Volume and add a file',
    summary: 'The file screen before a handle, the gate that opens one, and the modal that adds a file.',
    steps: [
      { sceneId: 'compose-volumes-live', note: 'web resolves to six Volume shapes, one of them absent.' },
      { sceneId: 'volume-explorer-no-handle', via: { kind: 'nav', label: 'Open 1:/app/uploads' }, note: 'A Handle exists only while someone has it open.' },
      { sceneId: 'start-handle-rw', via: { kind: 'nav', label: 'Open Volume' }, note: 'There is no confirmation gate after this dialog — the warning is it.' },
      { sceneId: 'volume-explorer-root', via: { kind: 'nav', label: 'Start the handle' }, note: 'The uploads root, with the escaping symlink and the 4.2 MB export.csv in it.' },
      { sceneId: 'volume-explorer-new-file', note: 'One modal serves New file and New folder.' },
    ],
  },
  {
    id: 'sign-out',
    label: 'Leave: the account menu',
    summary: 'The one control in the header that ends the session, and where it lands.',
    steps: [
      { sceneId: 'overview', note: 'The header is on every screen; its dropdown is reviewed once.' },
      { sceneId: 'shell-account-menu', note: 'Sign-out is separated from navigation on purpose.' },
      { sceneId: 'auth-signin', via: { kind: 'nav', label: 'Sign out' }, note: 'Signing out ends on the split screen, not an error page.' },
    ],
  },
];

/**
 * The declared link that walks `fromId` → `toId`, whatever kind it is: an
 * `opens` trigger is declared on the overlay and carries `from.scene` as its
 * parent, a `tab` on the screen whose tab bar shows the pane. Both reduce to
 * "this pair is linked, and this is the label the registry gives it".
 */
export function linkBetween(fromId: string, toId: string, scenes: readonly Scene[]): SceneLink | undefined {
  for (const scene of scenes) {
    for (const link of scene.links ?? []) {
      if (link.from.scene === fromId && link.to === toId) return link;
    }
  }
  return undefined;
}

export type TaskProblemKind = 'unknown-scene' | 'missing-link' | 'redundant-nav' | 'task-shape';

export type TaskProblem = {
  taskId: string;
  kind: TaskProblemKind;
  /** What is wrong and what to do about it. */
  detail: string;
};

const MIN_STEPS = 3;
const MAX_STEPS = 8;

/** Every task is written against the registry it claims to walk. Pure. */
export function validateTasks(tasks: readonly Task[], scenes: readonly Scene[]): TaskProblem[] {
  const problems: TaskProblem[] = [];
  const seenTaskIds = new Set<string>();
  const sceneIds = new Set(scenes.map((scene) => scene.id));

  for (const task of tasks) {
    const shape = (detail: string) => problems.push({ taskId: task.id, kind: 'task-shape', detail });

    if (task.id.trim() === '') shape('task id is empty');
    else if (seenTaskIds.has(task.id)) shape(`task id "${task.id}" is declared twice`);
    seenTaskIds.add(task.id);

    if (task.label.trim() === '') shape('label is empty');
    if (task.summary.trim() === '') shape('summary is empty');
    if (task.steps.length < MIN_STEPS || task.steps.length > MAX_STEPS) {
      shape(`${task.steps.length} steps: a task carries ${MIN_STEPS}–${MAX_STEPS}`);
    }

    const seenScenes = new Set<string>();
    task.steps.forEach((step, index) => {
      if (step.sceneId.trim() === '') shape(`step ${index + 1} names no scene`);
      if (seenScenes.has(step.sceneId)) shape(`scene "${step.sceneId}" appears twice in one task`);
      seenScenes.add(step.sceneId);

      if (!sceneIds.has(step.sceneId)) {
        problems.push({ taskId: task.id, kind: 'unknown-scene', detail: `step ${index + 1}: no scene "${step.sceneId}" in the registry` });
        return;
      }

      if (index === 0) {
        if (step.via !== undefined) shape(`step 1 carries a via — the first step is arrived at from nowhere`);
        return;
      }

      const previous = task.steps[index - 1]!;
      if (!sceneIds.has(previous.sceneId)) return; // one unknown scene is enough
      const link = linkBetween(previous.sceneId, step.sceneId, scenes);

      if (step.via?.kind === 'nav') {
        if (step.via.label.trim() === '') shape(`step ${index + 1}: a nav hop needs a label naming the control`);
        if (link !== undefined) {
          problems.push({
            taskId: task.id,
            kind: 'redundant-nav',
            detail: `step ${index + 1}: ${previous.sceneId} → ${step.sceneId} declares a nav hop but the registry links it as "${link.label ?? link.kind}" — drop the via`,
          });
        }
        return;
      }

      if (link === undefined) {
        problems.push({
          taskId: task.id,
          kind: 'missing-link',
          detail: `step ${index + 1}: ${previous.sceneId} → ${step.sceneId} has no declared link; add via: { kind: 'nav', label } if it is menu or route navigation`,
        });
      }
    });
  }

  return problems;
}
