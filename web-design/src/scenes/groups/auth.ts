/**
 * Auth group — `#/signin`, `#/setup` and the route-level 404.
 *
 * Auth is reached by signing out, not from the menu, so these screens sit in
 * their own column and their `menuPath` names the flow instead of a menu item.
 * The route (not a key) is what selects them — `signedIn` is outside
 * `SceneOverrides`' reach by design (ticket 02, D5).
 *
 * Data-only (type-only import): `node --test` loads this module.
 */

import type { SceneGroupModule } from '../types';

const HEIGHT = 840;

export const auth: SceneGroupModule = {
  id: 'auth',
  scenes: (paths) => [
    {
      id: 'auth-signin',
      title: 'Sign in',
      group: 'auth',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.signIn,
      caption:
        'The split screen exists so sign-out does not feel like an error page: check the form is the focus and the left panel says what this panel is before asking for credentials.',
      menuPath: ['Auth', 'sign-in'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'auth-signin-submitting',
      title: 'Sign in — submitting',
      group: 'auth',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.signIn,
      ui: { 'auth.submitting': 1 },
      caption:
        'The button holds its own loading state for the length of the mock\'s 500 ms sign-in: check nothing else on the form disables, because backspacing the email mid-flight should stay possible.',
      menuPath: ['Auth', 'sign-in'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'auth-signin-errors',
      title: 'Sign in — validation errors',
      group: 'auth',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.signIn,
      after: 'submit:auth.signin.empty',
      caption:
        'Produced by the form\'s real submit with nothing typed: the errors sit under their fields, and check that nothing implies the credentials were wrong — nothing was sent.',
      menuPath: ['Auth', 'sign-in'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'auth-setup',
      title: 'Set up — create the admin',
      group: 'auth',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.setup,
      caption:
        'First-run, step 1 of 2: the Steps control and the password rules are visible before typing, because the confirm-password rule is the one people get told about only after failing it.',
      menuPath: ['Auth', 'set-up'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'auth-setup-errors',
      title: 'Set up — mismatched confirmation',
      group: 'auth',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.setup,
      after: 'submit:auth.setup.mismatchedConfirm',
      caption:
        'The only state no click sequence reaches today (the mock\'s submit skips cross-field validation), produced by the real handler with a mismatched confirm: check "Passwords do not match" lands on the confirm field, not the password.',
      menuPath: ['Auth', 'set-up'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'auth-setup-ready',
      title: 'Set up — done',
      group: 'auth',
      kind: 'screen',
      viewports: ['desktop'],
      hash: paths.setup,
      ui: { 'auth.setupStep': 1 },
      after: 'toast:auth.adminCreated',
      caption:
        'Step 2 with the app\'s own "Admin account created" toast held open: check the only way off this screen is forward, because there is no admin to sign in as before this succeeds.',
      menuPath: ['Auth', 'set-up'],
      size: { width: 1600, height: HEIGHT },
    },
    {
      id: 'not-found',
      title: 'Route not found',
      group: 'auth',
      kind: 'screen',
      viewports: ['desktop'],
      // Built from `paths` by concatenation: `#/projects/nope` fails the
      // projectId number parse, which is the honest way to reach the router's
      // own not-found without a route literal.
      hash: `${paths.projects}/nope`,
      caption:
        'The router\'s catch-all for a hash nothing owns: check it stays inside the shell and offers the overview, because a panel that 404s to a blank page has lost the operator twice.',
      menuPath: ['Auth', 'unknown-route'],
      size: { width: 1600, height: 620 },
    },
  ],
};
