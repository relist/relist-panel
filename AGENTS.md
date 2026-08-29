## Agent skills

### Issue tracker

Issues and specs live as local markdown files under `.scratch/<feature>/`. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-role vocabulary (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`), recorded as a `Status:` line per issue file. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context — `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.

### Commit messages

Conventional Commits, no scope (`feat: ...`, `fix: ...`, `chore: ...`); no `Co-Authored-By`/`Claude-Session` trailers. See `docs/agents/commit-conventions.md`.
