/**
 * The task flows' pure layout: `TASKS` + the registry in, sections of step
 * cards and connectors out (ticket 11). Render-free on purpose — this is what
 * the page draws against and what `scripts/flow.test.mjs` covers under plain
 * Node, the same split as `src/storyboard/layout.ts`.
 *
 * The one runtime import carries its `.ts` extension because plain Node
 * resolves only explicit paths, and the test imports this file directly
 * (`allowImportingTsExtensions` in tsconfig keeps that typed; Vite is happy
 * either way).
 *
 * Geometry: cards run left to right, `perRow` per row, and the hop that crosses
 * a row is emitted as a wrap connector — a chip, not a line, because a line
 * from the fourth card back to the first would cross everything it just
 * passed. Every non-wrap connector leaves the source card's right edge and
 * lands on the target's left edge, mid-height.
 */

import { linkBetween } from '../scenes/tasks.ts';
import type { Task } from '../scenes/tasks';
import type { Scene, SceneGroupId, SceneKind } from '../scenes/types';

export type FlowGeometry = {
  cardWidth: number;
  cardHeight: number;
  gapX: number;
  gapY: number;
  /** Cards per row; a task with more steps wraps. */
  perRow: number;
};

/**
 * Sized for the shell's content column (1280 px): four cards and three gaps
 * come to 1264. The page scrolls horizontally if a narrower viewport cannot
 * fit them, rather than re-flowing under the reader.
 */
export const FLOW_GEOMETRY: FlowGeometry = {
  cardWidth: 232,
  cardHeight: 132,
  gapX: 112,
  gapY: 108,
  perRow: 4,
};

export type FlowCard = {
  sceneId: string;
  /** 1-based, as shown on the badge. */
  step: number;
  title: string;
  kind: SceneKind;
  group: SceneGroupId;
  x: number;
  y: number;
  width: number;
  height: number;
  note?: string;
};

export type FlowConnectorAction = 'opens' | 'tab' | 'nav';

export type FlowConnector = {
  from: string;
  to: string;
  action: FlowConnectorAction;
  /** The registry link's own label, or the nav hop's declared one. */
  label: string;
  /** Edge endpoints, or — when `wrap` — the chip's anchor. */
  x1: number;
  y1: number;
  x2: number;
  y2: number;
  /** The hop crosses rows: drawn as a chip under the source card. */
  wrap: boolean;
};

export type FlowSection = {
  taskId: string;
  label: string;
  summary: string;
  cards: FlowCard[];
  connectors: FlowConnector[];
  width: number;
  height: number;
};

/** Lays every task out; pure, and tolerant of a task that names an unknown scene. */
export function computeFlows(
  tasks: readonly Task[],
  scenes: readonly Scene[],
  geometry: FlowGeometry = FLOW_GEOMETRY,
): FlowSection[] {
  const byId = new Map(scenes.map((scene) => [scene.id, scene]));

  return tasks.map((task) => {
    const cards: FlowCard[] = task.steps.map((step, index) => {
      const scene = byId.get(step.sceneId);
      const row = Math.floor(index / geometry.perRow);
      const column = index % geometry.perRow;
      return {
        sceneId: step.sceneId,
        step: index + 1,
        // Unknown ids render as themselves; validation is what fails loudly.
        title: scene?.title ?? step.sceneId,
        kind: scene?.kind ?? 'screen',
        group: scene?.group ?? 'overview',
        x: column * (geometry.cardWidth + geometry.gapX),
        y: row * (geometry.cardHeight + geometry.gapY),
        width: geometry.cardWidth,
        height: geometry.cardHeight,
        ...(step.note !== undefined ? { note: step.note } : {}),
      };
    });

    const connectors: FlowConnector[] = [];
    for (let index = 1; index < task.steps.length; index += 1) {
      const fromStep = task.steps[index - 1]!;
      const toStep = task.steps[index]!;
      const from = cards[index - 1]!;
      const to = cards[index]!;

      const link = linkBetween(fromStep.sceneId, toStep.sceneId, scenes);
      const nav = toStep.via?.kind === 'nav' ? toStep.via.label : undefined;
      // The link's own label wins: the registry is the one source for it.
      const label = link?.label ?? nav ?? byId.get(toStep.sceneId)?.title ?? toStep.sceneId;
      const action: FlowConnectorAction =
        link?.kind === 'opens' ? 'opens' : link?.kind === 'tab' ? 'tab' : 'nav';

      if (from.y === to.y) {
        connectors.push({
          from: fromStep.sceneId,
          to: toStep.sceneId,
          action,
          label,
          x1: from.x + from.width,
          y1: from.y + from.height / 2,
          x2: to.x,
          y2: to.y + to.height / 2,
          wrap: false,
        });
      } else {
        connectors.push({
          from: fromStep.sceneId,
          to: toStep.sceneId,
          action,
          label,
          // The chip hangs under the row's last card; the target is only
          // recorded, because the step badge already carries the order.
          x1: from.x,
          y1: from.y + from.height + 16,
          x2: to.x,
          y2: to.y,
          wrap: true,
        });
      }
    }

    const width = cards.reduce((max, card) => Math.max(max, card.x + card.width), 0);
    const height = cards.reduce((max, card) => Math.max(max, card.y + card.height), 0);
    return { taskId: task.id, label: task.label, summary: task.summary, cards, connectors, width, height };
  });
}
