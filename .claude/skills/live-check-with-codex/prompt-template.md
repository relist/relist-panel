# Live check: <feature> (relist-panel)

You are testing a change end to end on an isolated, already-running test install. Do NOT edit any source file, commit or push. Only use the environment described here. Reply in Thai, and keep the PASS/FAIL/SKIP words in the result table in English.

## What should happen
<3-8 bullets: the behaviour under test, from the spec. Name the spec path so the agent can read it.>

## Environment (already running — do not recreate)
Every value below comes from `<repo>/.data/e2e/env`. Run `source <repo>/.data/e2e/env` in every shell you use.
- Panel: `$PANEL_URL` (https, self-signed; use `curl -k` or ignore certificate errors). Login `$ADMIN_EMAIL` / `$ADMIN_PASSWORD`. API writes need the header `Origin: $PANEL_URL`.
- Docker: the env file sets `DOCKER_HOST` to this sandbox only. Never unset it, never use the default Docker host, and never run `docker compose`. Containers: `relist-panel` (data dir `/var/lib/relist-panel`) and `relist-traefik`.
- Traefik: deployed Domains use `*.localhost` hosts on `$E2E_HTTP_PORT` and `$E2E_HTTPS_PORT`, e.g. `curl http://app.localhost:$E2E_HTTP_PORT/` and `curl -k https://app.localhost:$E2E_HTTPS_PORT/`. An HTTP→HTTPS redirect's Location has no port; check only its status and scheme.
- Use your browser tool for UI steps. Save screenshots to `<out>/shots/NN-short-name.png`.
- Test app image, if you need one: `traefik/whoami` (echoes request headers, port 80).

## Checks (do all, in order; keep going after a FAIL)
1. <exact action> → <exact expected status / UI text>. Screenshot.
2. …

Expected noise you can ignore: <e.g. 404 on a Compose's /source, 502 on /volumes/orphans>.

## At the end
- Leave the sandbox running.
- Final report (in Thai):
  1. A table with one row per check: # | PASS / FAIL / SKIP | evidence (status codes, short output) | screenshot path.
  2. For each FAIL: the exact steps, expected and actual results, and relevant logs (`docker logs relist-panel --since 10m | tail -50`).
  3. Any UI problems you noticed, even on checks that passed.
  4. No password values anywhere in the report.
