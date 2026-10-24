# 05 — Short guidance under the Compose editor

**What to build:** a 3–4 line note under the Monaco editor on the Compose page listing what to
avoid. Static text; the dynamic part is ticket 04.

- Containers should not write into `./src` — use a named volume
- To share a volume between Composes, declare it `external: true`
- Relative bind mounts such as `./data` live in the Compose's directory and are deleted with the Compose

**Blocked by:** none

**Status:** ready-for-agent

- [ ] Follow AGENTS.md: read Ant Design's `llms.txt` and the relevant component page (Typography /
      Alert) against the installed `antd` 6.5.1, and `docs/GUIDELINES.md`
- [ ] Collapsed or low-emphasis so it does not crowd the editor; readable at 320 px width
- [ ] Component test that the three points render

## Source

Stability review, items B3/B4; discussion on 2026-10-24.
