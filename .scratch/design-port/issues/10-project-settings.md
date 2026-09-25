# 10 — Project Settings tab (the `Manage` affordance's real home)

**What to build:** The design's Project screen (GUIDELINES §4.4) holds Environment create/delete in
the **Settings** tab, not in a header dropdown. Ticket 06 shipped the `Composes | Variables |
Settings` shell with Settings as a placeholder and a temporary `Manage` dropdown next to the
Environment `Segmented` (its Comments record the deferral). This ticket finishes the screen:
the Settings tab carries the Project form (rename/description), the Environment list with
create/delete (`DangerModal` with honest teardown copy — today Environment delete removes the
record only), and the Project danger zone (delete Project, `DangerModal` naming what disappears).
When it lands, the `Manage` dropdown is retired.

**Blocked by: 06 (done)**

**Status:** closed

- [x] The Settings tab renders the Project form, the Environment list with create/delete, and the
      Project danger zone, per the design's Project screen
- [x] The `Manage` dropdown is removed; Environment create/delete live in Settings
- [x] Destructive actions use `DangerModal` with copy that matches what the API actually tears down
- [x] No feature lost against the monolith's Project/Environment management

## Comments

Cut by the orchestrator on 2026-09-25: the design-port and host-monitoring code reviews both
flagged that no ticket covered the Settings tab's contents, and ticket 06 shipped the `Manage`
dropdown as an explicitly temporary affordance.

Grilled 2026-09-25 and settled: build it, priority low — `Manage` works today, this closes the
last design-vs-app gap. **Environment delete stays record-only** (grill decision): deleting an
Environment removes records, not running containers — someone tidying a staging environment
must not be able to kill production containers hiding in it, and the safe path to a teardown is
deleting Composes one by one (each with its own honest `DangerModal`) and then the empty
Environment. The modal copy says this plainly, as ticket 06's already does. Project delete keeps
the same semantics.

Implemented in this commit (`feat: build the Project Settings tab`).

- `pages/ProjectSettingsTab.tsx` is the design's Settings tab (§4.4), replacing ticket 06's
  placeholder: the Project details card (name + description form with the ADR-0005 rename note,
  `Save changes` on the store's `save-project:<id>` pending key), the Environments card (table:
  name, Compose count, Created, per-row delete with an `aria-label` per §10, plus a
  `New Environment` button in the card's extra that opens the page's one shared create modal),
  and the danger zone (error-bordered card + `Delete Project` via `DangerModal`, navigating to the
  Projects list). No endpoint gap: `updateProject`/`deleteProject`/`createEnvironment`/
  `deleteEnvironment` all existed in `api.ts`/`store.tsx`; nothing there was extended.
- Delete copy derives from two pure helpers in `pages/project-view.ts`, unit-tested (4 new tests,
  137 total): `environmentDeleteFacts`/`projectDeleteFacts` count the records and name the on-disk
  prefix from the first Compose's frozen slugs (ADR-0005 — read, never re-derived from current
  names), so both modals say plainly that Environment/Project delete removes records only (files
  stay on disk, running containers are not stopped) and point at deleting Composes one by one as
  the teardown path — the grill decision above. The Environment modal's copy is ticket 06's, moved
  verbatim; Project delete keeps the same semantics.
- The `Manage` dropdown is retired: `manageItems`, the header-row `Dropdown` and the placeholder
  card are gone from `ProjectPage.tsx`; Environment delete state/modal moved into the tab,
  keeping the fallback navigation when the active Environment is deleted. Deleting the last
  Environment lands on the page-level "No Environments yet" empty state (the design hides the tab
  row at zero Environments, so an empty list *inside* the card is unreachable by design).
- Monolith parity (`git show d11786a^:web/src/main.tsx`): rename + description save, Environment
  create (modal + suggestion tags) and delete, Project delete, and the Environment's created date
  are all present; restored the monolith's `max: 255` rule on the Environment name, which ticket
  06 had dropped — the only loss found. Project rename/delete also remain on the Projects list
  (§4.3 row menu), as the design has it.
- Deviations, deliberate: the Environments card carries a `New Environment` button in its extra
  (the prototype keeps create only in the page header; this ticket's "create/delete live in
  Settings" asked for both to be reachable there — the header button stays too); the details form
  adds `max: 255` on the Project name (monolith parity; the prototype's Settings form validates
  required only); submission failures join the page's closable error Alert (GUIDELINES §8,
  ticket-06 pattern) rather than toasts; the form's prefill effect keys on the Project's
  id/name/description instead of the object identity, so an unrelated tree patch cannot clobber a
  half-edited form.
- Verification: `pnpm --dir web typecheck`, `test` (137), `build` and `go test ./...` green;
  embedded dist rebuilt. Render checks ran the built bundle through the file:// CDP harness
  (single-file inlined build at a route-shaped path under an absolute `--base` the router honours
  as its basename, method-aware fetch shim over an in-memory tree, `Page.setDocumentContent`, one
  Chrome process per scenario, history rewrites swallowed so react-router's pushState fallback
  `location.assign` cannot wipe the synthetic document): 57 checks across 9 scenarios — Settings
  baseline (form prefilled, Environment rows with counts and Created, danger zone, Segmented
  intact), rename end-to-end (PUT recorded, header updates), empty-name validation (no PUT),
  failed save as an inline Alert, Environment create from the card (POST, row + Segmented gain
  `preview`), delete of an inactive Environment (record-only copy naming `dataDir/my-saas/staging/`,
  typed name gates the confirm, row gone, still on Settings), delete of the last, Compose-less
  Environment ("It holds no Composes.", page empty state), Project delete (record-only copy naming
  counts and `dataDir/my-saas/`, DELETE recorded, lands on the Projects list without the Project),
  and `Manage` gone from the switcher row on both the Settings and Composes tabs. Screenshots
  reviewed against §4.4.
- For a human: nothing open from this ticket. The Projects list keeps its own rename/delete
  affordances by design (§4.3); both entry points share the store methods.
