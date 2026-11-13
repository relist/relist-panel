---
name: live-check-with-codex
description: Use when a relist-panel change needs a manual, browser or live end-to-end check (UI clicks, curl through Traefik, real deploys), when the user asks to hand testing to Codex CLI or another agent ("codex", "terra", "ให้ codex เทส", "ให้ sonnet เทส"), or when you need to run a test panel server.
---

# Live check with Codex

## Overview
You start this checkout's isolated sandbox and write the checks. Codex CLI (or a subagent) runs them headless in a browser and with curl, then reports PASS/FAIL. Afterwards you turn the important checks into Playwright specs so they never need a manual run again. The rules are in `docs/agents/live-checks.md`; read it first.

## Pick the server

| The check needs | Server | Start |
|---|---|---|
| Anything with a real install: deploys, Traefik routing, labels, files Traefik reads, or just the UI | this checkout's e2e sandbox | `task e2e:serve` |
| Real data or upgrade migrations | a sandbox seeded from a copy of the live `panel.db` | `SEED_DB=… task e2e:serve` (see `docs/agents/live-checks.md`) |
| Server migration between hosts | two dind hosts | `task sandbox:migration` (see the script header) |

`task e2e:serve` blocks until the panel answers through Traefik. The first run takes about a minute, mostly the image build, and later runs take seconds. It writes `.data/e2e/env` and `.data/e2e/env.json`. Every value Codex needs comes from there: the panel URL, both Traefik ports, `DOCKER_HOST`/`DOCKER_CONFIG` and the admin login. `task e2e:reload` puts a code change into the running panel and keeps its data.

## Hand off
1. Run `task e2e:serve`, then `source .data/e2e/env`. Confirm `curl -sk $PANEL_URL/healthz` returns 200.
2. Check `e2e/tests/` for checks that a spec already covers. Run those with `task e2e`, and leave them out of the prompt.
3. Fill `prompt-template.md` from `.data/e2e/env` and save it in the scratchpad. Use numbered checks with exact inputs and the expected status codes or UI text.
4. Run in the background:
   `bash .claude/skills/live-check-with-codex/run-codex.sh $S/prompt.md $S`
   For a Claude subagent, pass the same prompt to the Agent tool instead. Its Playwright needs `pnpm exec playwright install chromium` once.
5. Read the head of `$S/codex-run.log` once to confirm the model, effort and sandbox. Do not poll it.
6. When it finishes, read `$S/codex-result.md` and open at least two screenshots yourself.
7. Reproduce each FAIL yourself before fixing it. After the fix, run `task e2e:reload`, re-run that check yourself, and report it as re-verified.
8. Turn the checks that matter into `e2e/tests/<feature>.spec.ts`, following "e2e specs" in `docs/agents/live-checks.md`. Run `task e2e -- <feature>` twice on the same sandbox: the second run proves the spec can run again.

Notes:
- `codex` is in `~/.local/bin`, and model ids are in `~/.codex/models_cache.json`.
- Codex has its own browser tool and needs nothing installed.
- About 15 checks take 15–25 minutes, and you are notified when it exits.
- Codex replies in Thai. A startup warning about `~/.codex/logs_2.sqlite` is harmless.
- **Role checks:** tell the agent to invite a user from the Users page, grant a Role, and open the invite link in a second browser context.

## Common mistakes
- **Guessing ports or hard-coding them in a prompt or a spec.** Read `.data/e2e/env`. Ports change when the sandbox restarts.
- **Reaching the host's default Docker.** It runs the user's live install. Use only the sandbox's `DOCKER_HOST`.
- **Running `relist-panel` inside the live container.** Old builds start a second server.
- **Stopping at "Codex says PASS".** Check its screenshots yourself.
- **Leaving secrets in screenshots or specs.** Screenshots of revealed passwords stay in the scratchpad. Never commit or publish them, and never put a seeded live database in a spec.
- **Removing the sandbox without asking, or telling Codex to tear it down.** Ask, then run `task e2e:down`.
- **Ending with a manual check only.** If nothing lands in `e2e/`, the next change needs the same manual check again.
