/**
 * Feedback group — the states gallery's sections, salvaged as scenes.
 *
 * These are the states with no home screen (the spec's "Feedback and states"),
 * addressed through `states.section` on the gallery's own route: one key, one
 * slug per section, so the board can frame each section without the gallery
 * growing a page per state. The gallery's volume-specific sections are dropped
 * on purpose — real Volume Explorer scenes cover them now.
 *
 * `kind` is `feedback` for all of them: the gallery is a review host, not a
 * home screen. Data-only (type-only import): `node --test` loads this module.
 */

import type { SceneGroupModule } from '../types';

const HEIGHT = 900;

export const feedback: SceneGroupModule = {
  id: 'feedback',
  scenes: (paths) => [
    {
      id: 'feedback-loading-first',
      title: 'Feedback — loading, first paint',
      group: 'feedback',
      kind: 'feedback',
      viewports: ['desktop'],
      hash: paths.states,
      ui: { 'states.section': 'loading-first' },
      caption:
        'What a screen shows before any data exists: check the skeleton occupies the content\'s real shape, because a first paint of nothing is how a slow panel becomes a judged one.',
      menuPath: ['Design system', 'States gallery'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'feedback-loading-actions',
      title: 'Feedback — loading, actions and refetches',
      group: 'feedback',
      kind: 'feedback',
      viewports: ['desktop'],
      hash: paths.states,
      ui: { 'states.section': 'loading-actions' },
      caption:
        'Buttons that spin without blanking their card: a refetch must keep the old data readable while it checks, or every refresh feels like a crash.',
      menuPath: ['Design system', 'States gallery'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'feedback-empty',
      title: 'Feedback — empty states',
      group: 'feedback',
      kind: 'feedback',
      viewports: ['desktop'],
      hash: paths.states,
      ui: { 'states.section': 'empty' },
      caption:
        'Three empties side by side (Projects, Composes, Variables), each selling its own next action: check no two of them are interchangeable, because "nothing yet" means something different in each.',
      menuPath: ['Design system', 'States gallery'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'feedback-empty-filtered',
      title: 'Feedback — filtered to nothing',
      group: 'feedback',
      kind: 'feedback',
      viewports: ['desktop'],
      hash: paths.states,
      ui: { 'states.section': 'empty-filtered' },
      caption:
        'The empty that quotes your query back: a filtered zero must read as "no match for this", distinct from the true empty beside it.',
      menuPath: ['Design system', 'States gallery'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'feedback-error-inline',
      title: 'Feedback — inline error, with a way out',
      group: 'feedback',
      kind: 'feedback',
      viewports: ['desktop'],
      hash: paths.states,
      ui: { 'states.section': 'error-inline' },
      caption:
        'A failure that keeps the page around it: check the retry is the offered way out and the rest of the screen stays usable, which is what separates an inline error from a page-level one.',
      menuPath: ['Design system', 'States gallery'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'feedback-error-page',
      title: 'Feedback — page-level error',
      group: 'feedback',
      kind: 'feedback',
      viewports: ['desktop'],
      hash: paths.states,
      ui: { 'states.section': 'error-page' },
      caption:
        'The screen-size failure ("Could not contact the panel"): check it says what to do next in plain words, because this state is reached exactly when nothing else on the page can help.',
      menuPath: ['Design system', 'States gallery'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'feedback-form-validation',
      title: 'Feedback — form validation',
      group: 'feedback',
      kind: 'feedback',
      viewports: ['desktop'],
      hash: paths.states,
      ui: { 'states.section': 'form-validation' },
      caption:
        'Errors painted on a real form (the gallery\'s demo submit): check each message sits under its own field and names the rule, not just "invalid".',
      menuPath: ['Design system', 'States gallery'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'feedback-toast',
      title: 'Feedback — outcome toasts',
      group: 'feedback',
      kind: 'feedback',
      viewports: ['desktop'],
      hash: paths.states,
      ui: { 'states.section': 'feedback-toast' },
      after: 'toast:states.deployed',
      caption:
        'The app\'s own success copy held open past its 3 s timeout through the toast adapter: check a toast says what happened and where to find it, and that it can be dismissed.',
      menuPath: ['Design system', 'States gallery'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'feedback-notification',
      title: 'Feedback — keyed notification',
      group: 'feedback',
      kind: 'feedback',
      viewports: ['desktop'],
      hash: paths.states,
      ui: { 'states.section': 'feedback-notification' },
      after: 'toast:states.deployNotification',
      caption:
        'The two-beat deploy notification (started, then finished under the same key): check the second beat replaces the first instead of stacking, which is what the key buys.',
      menuPath: ['Design system', 'States gallery'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'feedback-destructive',
      title: 'Feedback — destructive confirmations',
      group: 'feedback',
      kind: 'feedback',
      viewports: ['desktop'],
      hash: paths.states,
      ui: { 'states.section': 'destructive', 'states.dangerOpen': 1 },
      caption:
        'The typed-name modal over its demo copy: check the consequence sentence and the disabled-until-match OK are one pattern, because every destructive surface in the panel inherits it.',
      menuPath: ['Design system', 'States gallery'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'feedback-narrow',
      title: 'Feedback — narrow viewports',
      group: 'feedback',
      kind: 'feedback',
      viewports: ['desktop'],
      hash: paths.states,
      ui: { 'states.section': 'narrow' },
      caption:
        'The gallery\'s own 360 px demo, framed at desktop width: it documents what phone widths must answer, and stays a desktop frame until ticket 03 designs the real 390 scenes.',
      menuPath: ['Design system', 'States gallery'],
      size: { width: 1600, height: HEIGHT },
    },
  ],
};
