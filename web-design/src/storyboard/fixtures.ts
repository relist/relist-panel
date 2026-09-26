/**
 * Data-only fixtures for the storyboard engine — no JSX, no React, importable
 * by `node --test`. They exercise the whole grammar: three groups, overlay
 * rails (including two overlays on one parent), tab panes, `opens` triggers
 * and a menu whose items name every group.
 */

import type { BoardGroup, BoardMenuItem, BoardScene } from './types';

export const fixtureGroups: BoardGroup[] = [
  { id: 'core', label: 'Core flow' },
  { id: 'ops', label: 'Operations' },
  { id: 'feedback', label: 'Feedback' },
];

export const fixtureScenes: BoardScene[] = [
  {
    id: 'projects',
    title: 'Projects',
    groupId: 'core',
    kind: 'screen',
    size: { width: 1200, height: 780 },
    caption: 'Landing grid of projects',
    opens: [{ anchor: [0.97, 0.05], targetId: 'command-palette', label: 'search' }],
  },
  {
    id: 'command-palette',
    title: 'Command palette',
    groupId: 'core',
    kind: 'overlay',
    parentId: 'projects',
    size: { width: 460, height: 520 },
    caption: 'Opens over Projects',
  },
  {
    id: 'share-dialog',
    title: 'Share dialog',
    groupId: 'core',
    kind: 'overlay',
    parentId: 'projects',
    size: { width: 420, height: 320 },
    caption: 'Second overlay of Projects — stacks under the palette',
  },
  {
    id: 'project-detail',
    title: 'Project detail',
    groupId: 'core',
    kind: 'screen',
    size: { width: 1200, height: 820 },
    caption: 'Environments of one project',
    opens: [{ anchor: [0.96, 0.07], targetId: 'env-drawer', label: 'new environment' }],
  },
  {
    id: 'env-drawer',
    title: 'Environment drawer',
    groupId: 'core',
    kind: 'overlay',
    parentId: 'project-detail',
    size: { width: 480, height: 820 },
  },
  {
    id: 'compose',
    title: 'Compose',
    groupId: 'core',
    kind: 'screen',
    size: { width: 1280, height: 860 },
    caption: 'One compose, three panes',
    opens: [{ anchor: [0.97, 0.04], targetId: 'delete-dialog', label: 'delete' }],
    tabs: [
      { label: 'YAML', targetId: 'compose-yaml' },
      { label: 'Logs', targetId: 'compose-logs' },
    ],
  },
  {
    id: 'compose-yaml',
    title: 'Compose — YAML pane',
    groupId: 'core',
    kind: 'screen',
    size: { width: 1280, height: 860 },
    caption: 'Reached by the YAML tab',
  },
  {
    id: 'compose-logs',
    title: 'Compose — Logs pane',
    groupId: 'core',
    kind: 'screen',
    size: { width: 1280, height: 860 },
    caption: 'Reached by the Logs tab',
  },
  {
    id: 'delete-dialog',
    title: 'Delete confirm',
    groupId: 'core',
    kind: 'overlay',
    parentId: 'compose',
    size: { width: 520, height: 340 },
  },
  {
    id: 'deployments',
    title: 'Deployments',
    groupId: 'ops',
    kind: 'screen',
    size: { width: 1200, height: 760 },
  },
  {
    id: 'activity',
    title: 'Activity',
    groupId: 'ops',
    kind: 'screen',
    size: { width: 1200, height: 700 },
  },
  {
    id: 'review-notes',
    title: 'Review notes',
    groupId: 'feedback',
    kind: 'feedback',
    size: { width: 900, height: 640 },
    caption: 'Feedback quotes reference scene ids',
  },
];

export const fixtureMenu: BoardMenuItem[] = [
  { id: 'menu-projects', label: 'Projects', targetGroupId: 'core' },
  { id: 'menu-ops', label: 'Operations', targetGroupId: 'ops' },
  { id: 'menu-feedback', label: 'Feedback', targetGroupId: 'feedback' },
];
