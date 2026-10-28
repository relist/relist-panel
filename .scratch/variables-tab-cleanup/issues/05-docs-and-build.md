# 05 — GUIDELINES and embedded frontend

Status: ready-for-agent

**What to build:** First read `../spec.md`. Then update `docs/GUIDELINES.md` to match what tickets
01–04 shipped.

- **§4.5:** only the paste rule changes: a paste keeps an existing key's secret flag, and a new key
  follows the key-name heuristic. The Environment list is otherwise unchanged.
- **§4.6 Variables tab:**
  - Rewrite the three-card description:
    - Compose variables, with an override tag and Paste .env (replacing "no paste-in").
    - Variable Sets as `line` tabs, with New set in the card header, Rename/Delete in the tab bar, a
      read-only "Bound to" line and Paste .env.
    - The Compose page's tables: no secret/plain column, row actions on hover or focus (always on
      touch devices), filter above 10 rows.
    - Service bindings as a table with an icon Effective env button.
  - Card explanations live in ⓘ tooltips, and only the banner says "next deploy".
- Rebuild the embedded frontend (`task build-web`) and commit it separately (`chore: rebuild the
  embedded frontend`), as earlier tickets did.

**Blocked by:** 01, 02, 03, 04

- [ ] §4.5 and §4.6 match the shipped UI
- [ ] Embedded frontend rebuilt in its own commit
- [ ] Browser check at desktop and phone width per the spec's Verification section

## Comments
