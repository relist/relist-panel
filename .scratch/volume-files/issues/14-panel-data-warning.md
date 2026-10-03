# 14 — Warn when a Compose binds the panel's own data directory

**What to build:** the disclosure the spec's "one invariant" section promises. A Compose binding
the panel's data directory is allowed — the operator declared it — but the Explorer must warn that
editing `panel.db` can break the panel.

**Blocked by:** none

**Status:** ready-for-agent

- [ ] The Volumes payload marks the Volume whose resolved bind source is the panel's data directory
      (an exact match against `dataDir`; a bind below or beside it is not "the data directory")
- [ ] The Explorer shows the warning before the first operation, naming `panel.db` and the
      consequence — the Explorer is where the file can be edited
- [ ] HTTP-seam test: a bind at `dataDir` carries the mark; a bind inside `dataDir`, a bind
      outside it, and a named volume do not

## Source

Open item from the review of `7f42e5a...HEAD`: the spec requires the warning; the implementation,
`docs/GUIDELINES.md` §4.7 and every ticket dropped it. If the disclosure is judged not worth the
plumbing, amend the spec instead and close this `wontfix`.
