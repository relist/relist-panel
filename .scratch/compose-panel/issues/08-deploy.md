# 08 — Deploy

**What to build:** An operator can deploy a Compose. The panel injects the shared Traefik network and this Environment's variables, then actually brings the containers up via `docker compose`. This ticket establishes the `ComposeRunner` port that tickets 09–12 all build on.

**Blocked by:** 06, 07

**Status:** ready-for-agent

- [ ] The `ComposeRunner` interface (`Up`/`Down`/`Restart`/`Status`/`Logs`) is declared in the `compose` package per ADR-0003; a fake implementation used by tests records what it was asked to do
- [ ] The real `dockerexec` implementation shells out to the `docker compose` CLI over the mounted Docker socket
- [ ] Before deploying, the panel parses the Compose's YAML and injects the shared network into every service (merging with any existing `networks:` block), per ADR-0002
- [ ] The panel writes the resolved YAML and a generated `.env` file (from this Environment's Environment Variables) to `<data dir>/<project>/<environment>/<compose>/`
- [ ] Deploy runs under a deterministic Compose project name (`<project-slug>-<environment-slug>-<compose-slug>`) via `docker compose -p`
- [ ] Clicking "Deploy" (per `ComposeGeneral.dc.html`) runs `docker compose up -d --build` for real and the UI reflects success
- [ ] The status badge queries real container state ("N/M running") on page load and via a manual refresh button — no background polling
- [ ] Covered by HTTP-seam tests using the fake `ComposeRunner`, per the spec's Testing Decisions
