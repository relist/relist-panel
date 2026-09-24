# 06 — Project, reshaped

**What to build:** The design's Project screen (GUIDELINES §4.4): an Environment `Segmented` (with a
"manage" affordance for create/delete) and `Composes | Variables | Settings` tabs in the path
(`/projects/:p/env/:e/:tab`), replacing the monolith's nested tabs. Includes Compose create/delete and
the Environment's Compose list.

**Blocked by: 01, 02, 03, 04**

**Status:** ready-for-agent

- [ ] The Environment segmented switches environments; the tab is in the URL and survives a reload
- [ ] The Composes tab lists each Compose with the status vocabulary, last deploy, and the design's
      row actions
- [ ] Create/delete Compose and create/delete Environment work, with `DangerModal` for the
      destructive one and its warning copy (containers, networks, volumes, files under a named path)
- [ ] Empty states are the design's, not antd defaults
- [ ] No feature lost against the monolith's Project detail screen

## Comments
