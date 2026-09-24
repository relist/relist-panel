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

**Status:** needs-triage

- [ ] The Settings tab renders the Project form, the Environment list with create/delete, and the
      Project danger zone, per the design's Project screen
- [ ] The `Manage` dropdown is removed; Environment create/delete live in Settings
- [ ] Destructive actions use `DangerModal` with copy that matches what the API actually tears down
- [ ] No feature lost against the monolith's Project/Environment management

## Comments

Cut by the orchestrator on 2026-09-25: the design-port and host-monitoring code reviews both
flagged that no ticket covered the Settings tab's contents, and ticket 06 shipped the `Manage`
dropdown as an explicitly temporary affordance. Whether to build it (and when) is a product call —
it is the last visible gap between the design and the ported app.
