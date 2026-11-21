# e2e sandbox Docker API over TCP + TLS Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reach a checkout's `docker:dind` sandbox through its own TLS-protected TCP port on `127.0.0.1` instead of a unix socket bind-mounted from `$XDG_RUNTIME_DIR`. The sandbox then works on Docker Desktop (macOS, and Windows through WSL) as well as Linux, and the root-owned socket dir after a reboot (PR #90) cannot happen.

**Why:**
- **Docker Desktop.** Its daemon runs in a VM. A unix socket that dind creates in a bind-mounted dir shows up on the host as a file but cannot be connected to.
- **Native Linux and WSL.** After a reboot empties the tmpfs runtime dir, Docker re-creates the missing bind-mount source as root. `down` and `serve` then need `sudo`.
- **TLS, not plain TCP.** Plain TCP on loopback would hand a privileged daemon to every local process, and to web pages through DNS rebinding.

**Facts checked in `docker:29-dind` (2026-11-16):**
- **The entrypoint already listens on TLS.** When dind's arguments are empty or start with `-`, `dockerd-entrypoint.sh` generates certs in `DOCKER_TLS_CERTDIR=/certs` and listens on `tcp://0.0.0.0:2376 --tlsverify` and on `/var/run/docker.sock`.
- **Client files.** They are in `/certs/client/{ca,cert,key}.pem`. `key.pem` is mode 0644 inside the container.
- **SANs.** The server cert's SANs include every IP from `ip -oneline address`, which covers `127.0.0.1`, plus `localhost`. Extra SANs can be added with `DOCKER_TLS_SAN`.
- **When certs change.** The public certs are regenerated on every start. The CA key is kept as long as `/certs` survives. `/certs` is not a declared volume, so it lives in the container's own layer: it survives stop and start, and is gone after `rm`.

**Architecture:**
- **Starting dind.**
  - `scripts/e2e-sandbox.sh` starts dind with no `--host` arguments, publishes `-p 127.0.0.1::2376`, and drops the socket bind mount and `--group`.
  - After every start (`serve` and `reload`), it copies `/certs/client` out to `.data/e2e/certs` with the script's existing `host cp`.
- **Reaching it.** It writes `DOCKER_HOST=tcp://127.0.0.1:<port>`, `DOCKER_TLS_VERIFY=1` and `DOCKER_CERT_PATH` into `.data/e2e/env` and `env.json`. Its own `dind()` helper uses the same three variables.
- **The e2e side.** `e2e/lib/sandbox.ts` exposes the TLS fields and a `dockerEnv()` helper. `playwright.config.ts` puts that env into `process.env` at load. A spec that already does `{ ...process.env, DOCKER_HOST, DOCKER_CONFIG }` (the specs on the open Databases branches) inherits the TLS variables unchanged.
- **Delivery:** one PR, branch `claude/e2e-dind-tls` on `main` (worktree `.claude/worktrees/e2e-dind-tls`, already created). Its first commit adds this plan at `.scratch/e2e-sandbox-tls/plan.md`. When it merges, close #90 unmerged.

**Tech Stack:** bash, `docker:29-dind`, the Docker CLI's TLS env, TypeScript (Playwright).

**Spec:** this file.

## Global Constraints

- **Port and address.** The TLS port is published on `127.0.0.1` only, with a Docker-picked host port (`-p 127.0.0.1::2376`), read back with the script's existing `host_port 2376`. Never `0.0.0.0`, never a fixed port.
- **No plain TCP.** Never start dind with `DOCKER_TLS_CERTDIR=''`, and never use port 2375.
- **Certs.**
  - They go in `.data/e2e/certs/` (`.data/` is already git-ignored): the dir is mode 700, `key.pem` mode 600, `ca.pem` and `cert.pem` mode 644.
  - They are copied again after every dind start, and removed by `down`.
- **Env file.** `.data/e2e/env` exports, in this order:
  - `E2E_SANDBOX`, `PANEL_URL`, `E2E_HTTP_PORT`, `E2E_HTTPS_PORT`;
  - `DOCKER_HOST=tcp://127.0.0.1:<port>`, `DOCKER_TLS_VERIFY=1`, `DOCKER_CERT_PATH=<abs path to .data/e2e/certs>`, `DOCKER_CONFIG`;
  - `NO_PROXY`, written literally as `export NO_PROXY="${NO_PROXY:+$NO_PROXY,}127.0.0.1,localhost"` so the user's own value is kept;
  - `ADMIN_EMAIL`, `ADMIN_PASSWORD`.
- **`env.json`** gains `"dockerTlsVerify": "1"` and `"dockerCertPath": "<abs path>"`. Its `dockerHost` becomes the `tcp://` URL.
- **A sandbox made by the old script** has no 2376 port binding. Both `serve` and `reload` must stop **before** they build or start anything, with exactly this message (exit 1):

  > `this sandbox was made by an older e2e-sandbox.sh (no TLS port). Run: task e2e:down, then task e2e:serve`
- **Unchanged:**
  - the `host()` helper, the sandbox name, the label `relist.e2e.root`, `ls`, and `SEED_DB`/`SEED_EMAIL`;
  - the HTTP and HTTPS ports;
  - everything inside dind (the panel still mounts dind's own `/var/run/docker.sock`).
- **Docker.**
  - Reach any sandbox only through its `.data/e2e/env`; never the host's default Docker yourself. The script's own `host` calls are fine.
  - Never run `task e2e:down` on another checkout's sandbox.
  - Test the old-sandbox case with a fake `docker` on `PATH`, not with a real old sandbox.
- **Commits.** Conventional Commits, no scope, with the repo's `Co-Authored-By` trailer.

## Review Focus

1. **The user's existing sandboxes are all old-style.** `serve` and `reload` refuse with the message above, and nothing is built or started first. Test in Task 1: a fake `docker` whose port bindings have no `2376/tcp`. The run exits 1 with the message, and the fake's log has no `start`, `build`, `compose` or `run`.
2. **dind restarts** (a WSL reboot, or `reload` of a stopped sandbox). New public certs are copied again, so the env keeps working. Test in Task 1: after `serve`, delete `.data/e2e/certs`, then run `reload`. The certs are back, and `source .data/e2e/env && docker info` works.
3. **A spec that builds its own env** as `{ ...process.env, DOCKER_HOST: env.dockerHost, DOCKER_CONFIG: env.dockerConfig }` still reaches dind. Test in Task 2: `e2e/tests/sandbox-docker.spec.ts`.
4. **A proxy is set in the user's shell** (`HTTPS_PROXY=http://127.0.0.1:9`, a dead port). `source .data/e2e/env && docker info` still works through `NO_PROXY`. Test in Task 1, Step 4.
5. **The TLS port without a client cert.** `curl -sk https://127.0.0.1:<port>/version` (no client cert) and `curl -s http://127.0.0.1:<port>/version` both fail; the daemon never answers them. Test in Task 1, Step 4.

---

### Task 1: The script talks TLS

**Files:**
- Modify: `scripts/e2e-sandbox.sh`: the header comment, `SOCK_DIR` (removed), `dind()`, `write_env`, `down`, `reload`, the start block, and two new helpers.

**Interfaces:**
- Produces:
  - `.data/e2e/env` and `.data/e2e/env.json`, as in Global Constraints;
  - `.data/e2e/certs/{ca,cert,key}.pem`.
- New helpers in the script:
  ```bash
  require_tls_port   # fail with the exact old-sandbox message unless the container's PortBindings has 2376/tcp
  copy_certs         # host cp "$NAME:/certs/client/." into $OUT/certs, wait until ca/cert/key.pem exist (≤60 s), set modes
  dind() { DOCKER_HOST="tcp://127.0.0.1:$(host_port 2376)" DOCKER_TLS_VERIFY=1 DOCKER_CERT_PATH="$OUT/certs" docker "$@"; }
  ```

**Order:**
- **New sandbox:** `host run … -p 127.0.0.1::80 -p 127.0.0.1::443 -p 127.0.0.1::2376 docker:29-dind`, with no command arguments, then `copy_certs`, then `wait_for 60 dind info`.
- **Existing sandbox** (`serve` or `reload`): `require_tls_port`, then `host start`, then `copy_certs`, then `wait_for 60 dind info`.
  - `require_tls_port` reads `host inspect -f '{{json .HostConfig.PortBindings}}'`, which works even while the container is stopped, and looks for `2376/tcp`.
- **Failure hints.** Every `fail` that used to print `DOCKER_HOST=unix://…` now says `source $OUT/env && docker logs relist-panel`.

- [ ] **Step 1: Write the throwaway old-sandbox check** (Review Focus 1)

  Keep it out of the repo, in a scratch dir:
  - a fake `docker` on `PATH` that logs its arguments;
  - `inspect -f …PortBindings…` answers `{"443/tcp":[…],"80/tcp":[…]}`, with no 2376;
  - every other `inspect` succeeds, and every other command exits 0.

  Run `SANDBOX_NAME=fake scripts/e2e-sandbox.sh reload`, then the same with `serve`.
  Expected on the current script: no such message. Record that.
- [ ] **Step 2: Implement the script changes**
- [ ] **Step 3: Run the old-sandbox check again**

  Expected: both runs exit 1 with the exact message, and the log has no `start`, `build`, `compose` or `run`.
- [ ] **Step 4: Real run in this worktree** (it gets its own new sandbox)
  1. `task e2e:serve`, then `source .data/e2e/env && docker info --format '{{.ServerVersion}}'` prints a version.
  2. `stat -c '%a %n' .data/e2e/certs .data/e2e/certs/*` gives 700 for the dir, 600 for `key.pem`, and 644 for the other files.
  3. **Review Focus 5:** with `P` the TLS port, `curl -sk --max-time 5 https://127.0.0.1:$P/version` and `curl -s --max-time 5 http://127.0.0.1:$P/version` both fail, with no JSON.
  4. **Review Focus 4:** `HTTPS_PROXY=http://127.0.0.1:9 HTTP_PROXY=http://127.0.0.1:9 bash -c 'source .data/e2e/env && docker info >/dev/null && echo ok'` prints `ok`.
  5. **Review Focus 2:** `rm -rf .data/e2e/certs && task e2e:reload`, then `source .data/e2e/env && docker ps` works.
  6. `bash -n scripts/e2e-sandbox.sh`; also run `shellcheck` if it is installed.
- [ ] **Step 5: Commit**: `fix: reach the e2e sandbox over TLS instead of a bind-mounted socket`

---

### Task 2: Playwright side

**Files:**
- Modify: `e2e/lib/sandbox.ts`, `e2e/playwright.config.ts`
- Create: `e2e/tests/sandbox-docker.spec.ts`

**Interfaces:**
- Consumes: `env.json` from Task 1.
- Produces:
  ```ts
  export type Sandbox = { /* existing fields */ dockerTlsVerify: string; dockerCertPath: string };
  /** The env a docker CLI child process needs to reach this sandbox. */
  export function dockerEnv(): Record<string, string>; // DOCKER_HOST, DOCKER_TLS_VERIFY, DOCKER_CERT_PATH, DOCKER_CONFIG, NO_PROXY (existing value kept, + "127.0.0.1,localhost")
  ```
  `playwright.config.ts` does `Object.assign(process.env, dockerEnv())` right after reading the sandbox, so workers and child processes inherit it.

- [ ] **Step 1: Write `sandbox-docker.spec.ts`** (Review Focus 3)

  ```ts
  test('a docker child process reaches the sandbox the way specs call it', () => {
    const env = sandbox();
    const out = execFileSync('docker', ['version', '--format', '{{.Server.Version}}'],
      { encoding: 'utf8', env: { ...process.env, DOCKER_HOST: env.dockerHost, DOCKER_CONFIG: env.dockerConfig } }).trim();
    expect(out).toMatch(/^\d+\.\d+/);
  });
  test('dockerEnv alone is enough', () => {
    const out = execFileSync('docker', ['ps', '--format', '{{.Names}}'], { encoding: 'utf8', env: { PATH: process.env.PATH!, ...dockerEnv() } });
    expect(out).toContain('relist-panel');
  });
  ```
- [ ] **Step 2: Run it**

  Run: `cd e2e && pnpm exec playwright test tests/sandbox-docker.spec.ts`
  Expected:
  - the first test fails while `playwright.config.ts` does not yet set `process.env` (a TLS or HTTP error);
  - the second fails to compile until `dockerEnv` exists.
- [ ] **Step 3: Implement it**
- [ ] **Step 4: Run the whole suite**

  Run: `task e2e`
  Expected: every spec passes, including the new one, and `relist-panel`'s `RestartCount` is unchanged.
- [ ] **Step 5: Commit**: `test: give e2e specs the sandbox's TLS docker env`

---

### Task 3: Docs

**Files:**
- Modify:
  - `docs/agents/live-checks.md`: rule 3, the variables table, and the commands;
  - `AGENTS.md`: the "Live checks" bullet, which says "reach Docker only through the sandbox's `DOCKER_HOST`" and becomes "only through `source .data/e2e/env`";
  - the header comment in `scripts/e2e-sandbox.sh` (if Task 1 left it).

The docs must say:
- **How to reach it.** The sandbox's Docker API is TLS on a loopback port. `source .data/e2e/env` sets `DOCKER_HOST`, `DOCKER_TLS_VERIFY`, `DOCKER_CERT_PATH`, `DOCKER_CONFIG` and `NO_PROXY`. Setting `DOCKER_HOST` alone fails with a TLS error.
- **Cert files.** They are in `.data/e2e/certs`. Anyone who can read them controls that sandbox's dind, so never copy them out of `.data/`.
- **Old sandboxes.** A sandbox made before this change must be recreated: `task e2e:down`, then `task e2e:serve`, after asking the user (rule 4 still holds).
- **Platforms.** Docker Desktop on macOS and on Windows (from a WSL shell) is supported. Plain Windows shells are not, because the script is bash.

- [ ] **Step 1: Edit the docs**
- [ ] **Step 2: Check them against the code**

  Every variable named in the docs appears in `write_env`, and the reverse. `grep -rn "unix://\|SOCK_DIR\|XDG_RUNTIME" scripts/e2e-sandbox.sh docs/agents AGENTS.md` prints nothing.
- [ ] **Step 3: Commit**: `docs: the e2e sandbox's docker env is TLS`

---

## After merge (not part of the tasks)

- **Close #90.**
- **Sandboxes made before this change**, in every worktree, need `task e2e:down` and then `task e2e:serve`. Ask the user first.
- **The specs on the open Databases branches** (#80–#88) need no change: they spread `process.env`, which now carries the TLS variables. Run their suites once after rebasing onto the new `main`.
- **Not verified by us:** Docker Desktop on macOS and on Windows. The user should run `task e2e:serve && task e2e` there once.
