## Agent skills

### Issue tracker

Issues and specs live as local markdown files under `.scratch/<feature>/`. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-role vocabulary (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`), recorded as a `Status:` line per issue file. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context — `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.

### Commit messages

Conventional Commits, no scope (`feat: ...`, `fix: ...`, `chore: ...`). See `docs/agents/commit-conventions.md`.

### Live checks and e2e tests

To run this checkout for real (browser, curl through Traefik, deploys), use `task e2e:serve`. Then read ports and credentials from `.data/e2e/env`, and never guess them. Run the Playwright specs with `task e2e`.

- Every checkout gets its own `docker:dind` sandbox, so worktrees never collide.
- The host's default Docker runs the user's live install: reach Docker only through the sandbox's `DOCKER_HOST`.
- Ask before `task e2e:down`.

When a manual check passes, add its important checks to `e2e/`. See `docs/agents/live-checks.md`.

### Frontend and Ant Design

Before creating or modifying frontend pages, components, layout, styling, or UI behavior in `web/`, **read [Ant Design's `llms.txt`](https://ant.design/llms.txt)**, then open the linked official component and design documentation relevant to the change. Use the index to select the relevant pages rather than loading the entire documentation bundle.

Check component APIs, deprecated props, and migration guidance against the installed `antd` version in `web/package.json`. Follow `docs/GUIDELINES.md` for this repo's UI contract alongside the official documentation.
