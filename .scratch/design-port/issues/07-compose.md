# 07 — Compose as its own route (Editor tab)

**What to build:** The design's Compose screen (GUIDELINES §4.6) as `…/compose/:composeId`: the
`Editor | Monitor` tab row (Monitor's content is 06 in the monitoring spec), Monaco on the left and
the status rail on the right in document flow, both stretched to the same height with
`LAYOUT.editorMinHeight` as the floor; the unsaved-changes banner and Save/Discard; the full-screen
editor modal (`FullscreenEditor`) sharing one Monaco model; the Logs drawer; Deploy identity,
Environment and the deploy/stop/restart/delete actions. Drop the Activity card (no events are
recorded — §11).

**Blocked by: 01, 02, 03, 04, 06**

**Status:** ready-for-agent

- [ ] Editor and rail stretch to the same height; no nested scrollers on the page (GUIDELINES §6); the
      tab row's path segment is linkable and the unknown-segment case falls back to Editor
- [ ] Editing marks the file dirty (tag, `Modified` marker, banner); Save persists; Discard reverts;
      deploying saves first
- [ ] The full-screen modal opens over the same model and closing it does not discard the draft
- [ ] The logs drawer filters by service and copies
- [ ] Deploy/stop/restart/delete work, with the deploy-identity card showing the frozen path and
      `docker compose -p` name (ADR-0005)
- [ ] No feature lost against the monolith's `ComposeDetailPanel`

## Comments
