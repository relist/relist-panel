/**
 * The task flows' contract (ticket 11) under `node --test` and nothing else:
 * `TASKS` written against the real registry, `validateTasks` failing loudly on
 * every way a task can drift, and `computeFlows` laying cards out without
 * overlaps and with connectors on card edges.
 *
 * The registry is rebuilt here exactly as `src/scenes/registry.ts` builds it
 * (same pattern as `scripts/registry.test.mjs`): `GROUPS` owns the order, each
 * `groups/<id>.ts` exports its scenes as a function of the router's real
 * `paths`, and `registry.ts` itself is not imported because its extensionless
 * runtime imports do not resolve under plain Node.
 *
 * Run: node --test scripts/flow.test.mjs
 */

import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import { parse, paths } from '../src/app/router.ts';
import { FLOW_GEOMETRY, computeFlows } from '../src/flow/layout.ts';
import { GROUPS } from '../src/scenes/groups.ts';
import { TASKS, linkBetween, validateTasks } from '../src/scenes/tasks.ts';

/** camelCase export name of a group module, derived from the group id. */
const exportName = (id) => id.replace(/-([a-z])/g, (_, character) => character.toUpperCase());

// Rebuild the registry exactly as registry.ts does: GROUPS owns the order.
const groupModules = await Promise.all(
  GROUPS.map(async (group) => ({
    group,
    scenes: await import(`../src/scenes/groups/${group.id}.ts`).then(
      (module) => module[exportName(group.id)].scenes,
    ),
  })),
);
const SCENES = groupModules.flatMap(({ scenes }) => scenes({ ...paths }));
const byId = new Map(SCENES.map((scene) => [scene.id, scene]));
const sections = computeFlows(TASKS, SCENES);
const sectionOf = (taskId) => sections.find((section) => section.taskId === taskId);

describe('TASKS — written against the registry', () => {
  it('passes validation with no problems at all', () => {
    assert.deepEqual(validateTasks(TASKS, SCENES), []);
  });

  it('declares unique ids and 3–8 steps each', () => {
    const ids = TASKS.map((task) => task.id);
    assert.equal(new Set(ids).size, ids.length, 'task ids must be unique');
    for (const task of TASKS) {
      assert.ok(task.steps.length >= 3 && task.steps.length <= 8, `${task.id} carries ${task.steps.length} steps`);
      assert.ok(task.label.trim() !== '' && task.summary.trim() !== '', `${task.id} needs a label and a summary`);
    }
  });

  it('never re-types a label the registry already carries', () => {
    for (const task of TASKS) {
      for (let index = 1; index < task.steps.length; index += 1) {
        const from = task.steps[index - 1].sceneId;
        const step = task.steps[index];
        if (step.via?.kind === 'nav') continue;
        const link = linkBetween(from, step.sceneId, SCENES);
        assert.ok(link !== undefined, `${task.id}: ${from} → ${step.sceneId} must be a declared link`);
        assert.ok(link.label !== undefined, `${task.id}: ${from} → ${step.sceneId} must carry a label`);
      }
    }
  });
});

describe('validateTasks — every way a task drifts is caught', () => {
  const base = (steps) => [{ id: 'probe', label: 'Probe', summary: 'A probe.', steps }];

  it('rejects a step that names no scene', () => {
    const problems = validateTasks(
      base([
        { sceneId: 'projects-cards' },
        { sceneId: 'nope' },
        { sceneId: 'project-composes', via: { kind: 'nav', label: 'x' } },
      ]),
      SCENES,
    );
    assert.deepEqual(problems.map((problem) => problem.kind), ['unknown-scene']);
  });

  it('rejects a hop with no link and no nav declaration', () => {
    const problems = validateTasks(
      base([{ sceneId: 'overview' }, { sceneId: 'monitoring' }, { sceneId: 'projects-cards' }]),
      SCENES,
    );
    assert.deepEqual(problems.map((problem) => problem.kind), ['missing-link', 'missing-link']);
  });

  it('rejects a nav hop where the registry already carries a link', () => {
    const problems = validateTasks(
      base([
        { sceneId: 'projects-cards' },
        { sceneId: 'projects-new-modal', via: { kind: 'nav', label: 'Open the modal' } },
        { sceneId: 'projects-new-modal-errors', via: { kind: 'link' } },
      ]),
      SCENES,
    );
    assert.deepEqual(problems.map((problem) => problem.kind), ['redundant-nav']);
  });

  it('rejects a two-step task, a repeated scene, a repeated id and a via on step 1', () => {
    const problems = validateTasks(
      [
        { id: 'probe', label: 'Probe', summary: 'A probe.', steps: [{ sceneId: 'overview', via: { kind: 'nav', label: 'x' } }, { sceneId: 'monitoring', via: { kind: 'nav', label: 'y' } }] },
        { id: 'probe', label: 'Probe', summary: 'A probe.', steps: [{ sceneId: 'overview' }, { sceneId: 'overview' }, { sceneId: 'monitoring' }] },
      ],
      SCENES,
    );
    const shapeDetails = problems.filter((problem) => problem.kind === 'task-shape').map((problem) => problem.detail);
    assert.ok(shapeDetails.some((detail) => detail.includes('2 steps')), 'a 2-step task must fail');
    assert.ok(shapeDetails.some((detail) => detail.includes('appears twice')), 'a repeated scene must fail');
    assert.ok(shapeDetails.some((detail) => detail.includes('declared twice')), 'a repeated task id must fail');
    assert.ok(shapeDetails.some((detail) => detail.includes('step 1')), 'a via on step 1 must fail');
  });
});

describe('computeFlows — geometry', () => {
  it('renders one card per step, in order, with the step badge', () => {
    for (const task of TASKS) {
      const section = sectionOf(task.id);
      assert.ok(section !== undefined, `${task.id} must have a section`);
      assert.deepEqual(
        section.cards.map((card) => card.sceneId),
        task.steps.map((step) => step.sceneId),
      );
      assert.deepEqual(
        section.cards.map((card) => card.step),
        task.steps.map((_, index) => index + 1),
      );
      assert.equal(section.connectors.length, task.steps.length - 1);
    }
  });

  it('takes card titles, kinds and groups from the registry', () => {
    for (const section of sections) {
      for (const card of section.cards) {
        const scene = byId.get(card.sceneId);
        assert.equal(card.title, scene.title);
        assert.equal(card.kind, scene.kind);
        assert.equal(card.group, scene.group);
      }
    }
  });

  it('never overlaps two cards and keeps them inside the section box', () => {
    for (const section of sections) {
      for (let a = 0; a < section.cards.length; a += 1) {
        for (let b = a + 1; b < section.cards.length; b += 1) {
          const first = section.cards[a];
          const second = section.cards[b];
          const overlap =
            first.x < second.x + second.width &&
            second.x < first.x + first.width &&
            first.y < second.y + second.height &&
            second.y < first.y + first.height;
          assert.ok(!overlap, `${section.taskId}: cards ${first.step} and ${second.step} overlap`);
        }
        const card = section.cards[a];
        assert.ok(card.x >= 0 && card.y >= 0 && card.x + card.width <= section.width);
        assert.ok(card.y + card.height <= section.height);
      }
    }
  });

  it('lays four cards per row and marks exactly the cross-row hops as wraps', () => {
    for (const section of sections) {
      const cards = section.cards;
      for (let index = 0; index < cards.length; index += 1) {
        const card = cards[index];
        const expectedRow = Math.floor(index / FLOW_GEOMETRY.perRow);
        assert.equal(card.y, expectedRow * (FLOW_GEOMETRY.cardHeight + FLOW_GEOMETRY.gapY));
        if (index % FLOW_GEOMETRY.perRow !== 0) {
          assert.ok(card.x > cards[index - 1].x, `${section.taskId}: steps advance left to right`);
        } else {
          assert.equal(card.x, 0, `${section.taskId}: a row starts at the section's left edge`);
        }
      }
      for (const connector of section.connectors) {
        const from = cards.find((card) => card.sceneId === connector.from);
        const to = cards.find((card) => card.sceneId === connector.to);
        assert.equal(connector.wrap, from.y !== to.y, `${section.taskId}: wrap must mean "crosses a row"`);
        if (!connector.wrap) {
          assert.equal(connector.x1, from.x + from.width);
          assert.equal(connector.y1, from.y + from.height / 2);
          assert.equal(connector.x2, to.x);
          assert.equal(connector.y2, to.y + to.height / 2);
        }
        assert.ok(connector.label.trim() !== '', `${section.taskId}: every connector is labelled`);
      }
    }
  });

  it('a five-step task wraps exactly once and labels the chip from the registry link', () => {
    const section = sectionOf('project-tabs');
    const wraps = section.connectors.filter((connector) => connector.wrap);
    assert.equal(wraps.length, 1);
    assert.equal(wraps[0].label, 'Delete Project');
    assert.equal(wraps[0].action, 'opens');
  });
});

describe('linkBetween — declared links in both directions of declaration', () => {
  it('resolves an opens trigger declared on the overlay back to its parent', () => {
    const link = linkBetween('projects-cards', 'projects-new-modal', SCENES);
    assert.equal(link.kind, 'opens');
    assert.equal(link.label, 'New Project');
  });

  it('resolves a tab declared on the screen whose tab bar shows the pane', () => {
    const link = linkBetween('project-variables', 'project-settings', SCENES);
    assert.equal(link.kind, 'tab');
    assert.equal(link.label, 'Settings');
  });

  it('returns nothing for a pair the registry does not connect', () => {
    assert.equal(linkBetween('overview', 'volume-explorer-root', SCENES), undefined);
  });
});

describe('router — the flow route parses like the board', () => {
  it('parses #/design/flow as a flow route and leaves the board alone', () => {
    assert.equal(parse('#/design/flow').kind, 'flow');
    assert.equal(parse('#/design/board').kind, 'board');
  });
});
