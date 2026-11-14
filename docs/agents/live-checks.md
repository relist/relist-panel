# Live checks and e2e tests

How an agent runs this checkout for real: browser checks, curl through Traefik, real deploys, and the Playwright specs in `e2e/`. Several worktrees and agents do this at the same time on one host, next to the user's **live install**. Everything below exists so they never collide and never touch it.

## The rules

1. **Start a sandbox with `task e2e:serve`, never by hand.** It runs this checkout's install stack inside one `docker:dind` container named `relist-e2e-<checkout dir>`. The panel, Traefik and every Compose a test deploys live inside it.
2. **Read ports and credentials from `.data/e2e/env`, or `.data/e2e/env.json`.** Never guess a port or write that file yourself.
   - The script writes it from `docker port` on every `serve` and `reload`, and `down` deletes it.
   - Docker picks both host ports itself, so two sandboxes never ask for the same one.
3. **Use Docker only through the sandbox.** Either `source .data/e2e/env`, which sets `DOCKER_HOST` to the sandbox's unix socket and `DOCKER_CONFIG` to an empty config, or `docker exec` on the sandbox container.
   - The host's default Docker runs the user's live install (`relist-panel-panel-1`, `relist-panel-traefik-1`) and other projects.
   - Never run `docker compose` in a checkout directory.
   - Never run the `relist-panel` binary inside the live container: old builds start a second server on any argument, `--help` included.
4. **Leave the sandbox running when you finish, and ask the user before `task e2e:down`.** The user may want to look at it. `down` removes only this checkout's sandbox.

## What a sandbox gives you

| Variable in `.data/e2e/env` | Use |
|---|---|
| `PANEL_URL` | `https://panel.localhost:<port>`. The panel is served through Traefik with a self-signed certificate (`curl -k`, Playwright `ignoreHTTPSErrors`). |
| `E2E_HTTP_PORT`, `E2E_HTTPS_PORT` | Traefik's entrypoints. Deployed Domains use `*.localhost` hosts: `curl http://app.localhost:$E2E_HTTP_PORT/`. Browsers and curl resolve `*.localhost` to 127.0.0.1; Node does not, so `e2e/lib/sandbox.ts` does it in its HTTP client. |
| `DOCKER_HOST`, `DOCKER_CONFIG` | The sandbox's Docker API. Containers inside it: `relist-panel` (data dir `/var/lib/relist-panel`) and `relist-traefik`. |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD` | The sandbox's admin account. |

Commands:

- `task e2e:serve`: start the sandbox, or reuse it. It builds this checkout into the sandbox each time, and takes about a minute the first time and a few seconds after that.
- `task e2e:reload`: put a code change into the running panel and keep its data.
- `task e2e:ls`: list every sandbox on the host and the checkout it belongs to.
- `task e2e:down`: remove this checkout's sandbox.

**To test on real data,** copy the live database out read-only:

1. `docker cp relist-panel-panel-1:/var/lib/relist-panel/panel.db .data/e2e-seed/panel.db`, then `chmod 600` it.
2. `task e2e:down`, then `SEED_DB=.data/e2e-seed/panel.db task e2e:serve`. The copy is migrated, and its admin's password and 2FA are reset in the copy only.

The copy holds real secrets: keep it under `.data/`, never commit it, never use it in a spec, and offer to delete it when done.

## e2e specs

`e2e/` is a Playwright Test package. It has its own `package.json`, and pnpm installs it.

- **Run them:** `task e2e` serves the sandbox, installs Chromium if needed and runs every spec. `task e2e -- basic-auth` runs one file. Specs run one at a time against the shared sandbox.
- **Write a spec so it runs again on the same sandbox:**
  - Name everything with `runId()`.
  - Set up through the API helpers in `e2e/lib/sandbox.ts` (`Panel.signIn`, `panel.json`, `panel.deploy`, `call`, `traefikUrl`).
  - Use the browser only for the UI behaviour under test.
  - Poll with `expect.poll` for anything Traefik applies asynchronously.
- **After a manual or agent-driven check passes,** turn its important checks into a spec in the same PR. Next time, the manual check covers only what no spec does yet.

## Running a manual check

Use this for anything no spec covers yet. Run it yourself with whatever browser tool you have (Playwright, a browser-use tool, curl), or hand the checklist to another agent. These steps do not depend on a particular tool.

1. Run `task e2e:serve`, then `source .data/e2e/env`. Confirm `curl -sk $PANEL_URL/healthz` returns 200.
2. Check `e2e/tests/` for checks a spec already covers. Run those with `task e2e`, and leave them out of the manual check.
3. Write the checklist before you start:
   - A few bullets on the behaviour under test, naming the spec or issue it comes from.
   - Numbered checks, each with the exact action and the exact expected result: status code, UI text or Docker state.
   - Expected noise to ignore, for example a 404 on a Compose's `/source`.
4. Run every check in order and keep going after a FAIL. Save screenshots in your scratchpad as `NN-short-name.png`.
5. Report one row per check: number, PASS / FAIL / SKIP, evidence (status codes, short output) and the screenshot path. For each FAIL, add the steps, the expected and actual results, and `docker logs relist-panel --since 10m | tail -50`.
6. Reproduce each FAIL yourself before fixing it. After the fix, run `task e2e:reload` and run that check again.
7. Turn the checks that matter into `e2e/tests/<feature>.spec.ts`, as in "e2e specs" above. Run `task e2e -- <feature>` twice on the same sandbox: the second run proves the spec can run again.

**When another agent runs the checklist,** give it everything it cannot find out on its own:
- Tell it to `source <repo>/.data/e2e/env` in every shell, and never to unset `DOCKER_HOST` or use the default Docker host.
- Tell it not to edit source files, commit, push or remove the sandbox.
- API writes need the header `Origin: $PANEL_URL`.
- An HTTP→HTTPS redirect's Location has no port, so check only its status and scheme.
- `traefik/whoami` is a test app image that echoes request headers on port 80.
- For Role checks: invite a user from the Users page, grant a Role, and open the invite link in a second browser context.

When it reports PASS, open at least two of its screenshots yourself.

**Common mistakes:**
- Guessing ports or hard-coding them in a checklist or a spec. Ports change when the sandbox restarts.
- Leaving secrets in screenshots or specs. Screenshots of revealed passwords stay in the scratchpad: never commit or publish them.
- Ending with a manual check only. If nothing lands in `e2e/`, the next change needs the same manual check again.
