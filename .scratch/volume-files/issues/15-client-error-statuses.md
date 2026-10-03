# 15 — Map helper client mistakes to 4xx instead of 502

**What to build:** the review's point that a missing entry (list/stat/read) and a symlink that
escapes the Volume are the request's fault, not the gateway's. Today every helper exit 1 answers
502 Bad Gateway.

**Blocked by:** none

**Status:** needs-triage

- [ ] Decide the protocol: a not-found exit code (6?) → 404, and an escape refusal → 4xx (422?),
      or keep 502 and say why in the spec. Ticket 05 currently calls the 502 "the panel's normal
      JSON error carrying the helper's own message" — a deliberate choice, not an oversight
- [ ] If changed: `volfs` sentinels, helper exit codes, `dockerexec` mapping, HTTP statuses, the
      Explorer's presentation and the spec's Protocol table all move together
- [ ] The Explorer already special-cases the escape refusal by message text
      (`isEscapeRefusal`); a status would replace that string match

## Source

Review of `7f42e5a...HEAD`, "Implemented but wrong" #4. The spec is silent; this is a design change
first and a small protocol extension second.
