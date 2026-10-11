# Spec — Sources and the deployment queue (build images from Git or a zip)

Date: 2026-10-11.
Status: needs-triage

## Problem

A Compose can only run images that already exist. ADR-0001 kept git builds out of scope, and the
glossary says the panel never builds an Image. An operator whose app lives in a Git repository has to
build on another machine, `docker save` the image, and load it from the Images page before every
deploy.

Deploys also run inside one HTTP request that holds the Project gate until `docker compose up`
returns (`internal/httpapi/coordination.go`). That is fine for `up`, which takes seconds, but a build
can take many minutes. Two builds at once can also exhaust a small host.

## Goal and success criteria

- A Compose can have a **Source**: a Git repository reached with an SSH deploy key the panel
  generates, or an uploaded zip of source code. Its code lives in `<compose dir>/src/`.
- The operator configures the Source from a **Source** card on the Editor tab. No creation wizard is
  added: every Compose is still created the old way.
- The panel finds Dockerfiles in the Source and offers to insert standard `build:` blocks into the
  editor draft. After that, the YAML belongs to the operator, as it does today.
- **Build & Deploy** fetches the Source, builds each Service that has `build:`, one at a time, then
  runs `up`. A failure before `up` leaves the running containers untouched.
- Every deploy, with or without a Source, is a **Deployment** that runs in a single host-wide queue,
  one at a time. Each has a live log, steps and a history of 20 per Compose.
- An existing Compose without a Source deploys exactly as before, through the queue.

## Out of scope

- Webhooks and auto-deploy on push. Deploys stay manual (ADR-0001).
- A GitHub App, OAuth, or HTTPS token auth. Git is SSH with a deploy key only.
- Builders other than a Dockerfile (Nixpacks, Railpack, Buildpacks, Static).
- Rollback to an earlier Deployment, and choosing a commit or tag to deploy.
- Automatic pruning of old images or BuildKit cache. Old builds become dangling images that
  `docker image prune` removes. A follow-up issue covers a "Prune build cache" action on the Images
  page and auto-prune settings.
- Dedicated build-args, build-secrets or `.env`-in-context fields. Compose's own `build.args` and
  `build.secrets` cover them (see Build args and secrets).
- Running builds in parallel.

## Language

Added to `CONTEXT.md`:

- **Source**: the source code of one Compose, from a Git repository or an uploaded zip, kept in
  `<compose dir>/src/`. A Compose has at most one. _Avoid_: Repo (a zip Source has none), App.
- **Deployment**: one queued run that brings a Compose's containers up to date: fetch the Source,
  build each Service that has `build:`, then `up`. Numbered per Compose (#1, #2, …), with a status,
  steps and a log. _Avoid_: Build (the build is one step of a Deployment), Job, Release.
- **Deploy key**: the SSH key pair the panel generates for one Compose's Git Source. The operator adds
  its public half to the repository as a read-only deploy key.

Changed: **Image** reads "A Compose references one by name in its file, or builds one from its
Source; the panel builds images only from a Source and never pulls one itself."

ADR-0016 records the decisions below and supersedes ADR-0001's "No separate git-based Application
type" non-goal. A Source is still not an Application: it is an optional part of a Compose.

## Decisions

### `build:` lives in the YAML, as standard Compose

The operator's YAML says what to build, with Compose's own `build:` key:

```yaml
services:
  api:
    build:
      context: ./src/api
      dockerfile: Dockerfile
      target: production   # optional build stage
```

The panel does not inject `build:` or `image:` at deploy, and no part of the editor is locked. A
wrong path fails the build with Docker's own error, in the Deployment log. Because `build:` is plain
Compose, the file can be copied and run outside the panel.

Rejected: a panel-managed list of build targets injected at deploy, with read-only decorations in the
editor. It means a second table that mirrors the YAML, and the YAML the operator sees would not be
the file that runs.

Compose names a built image `<docker compose project>-<service>:latest`, for example
`relist-c42-api:latest`, unless the Service sets `image:`. A rebuild moves that tag to the new image,
and the old one becomes dangling.

### The code lives in `./src`

Both kinds of Source put the code in `<compose dir>/src/`, next to the resolved
`docker-compose.yml`. Relative paths in the YAML resolve against the compose file's directory, so
`context: ./src/api` works for both. A separate directory keeps repository files away from the files
the panel writes itself (the resolved YAML, `.env`, `sets/`). This holds for legacy name-derived
Compose directories (ADR-0002) and ID-based ones (ADR-0011) alike.

`./src` is wiped by every fetch (`git clean -ffdx`) and every zip upload, so the panel must own it.
The panel creates `src/` itself with a marker file, `src/.relist-source`, which `git clean` keeps
through `-e .relist-source`. Creating a Source on a Compose whose directory already holds a `src`
without that marker is refused with `409`, "src/ already exists in this Compose's directory, move it
first", because an existing Compose may bind-mount `./src` for its own data. The panel's other new
paths are dotted, like `.traefik` and `.composes`: logs in `.deployments/`, staging files
`.build-compose.yml` and `.build.env`, and `.ssh/`.

### Git over SSH with a per-Compose deploy key

- The panel generates an ed25519 key pair in Go (`golang.org/x/crypto/ssh`) when a Git Source is
  first configured. The comment is `relist-c<composeID>`.
- GitHub refuses the same deploy key on two repositories, so every Compose has its own key.
- The private key is stored in the database, like a secret variable. Per CONTEXT.md, the panel's
  single admin owns the host, so this is not a security boundary.
- Every git command runs with
  `GIT_SSH_COMMAND="ssh -i <tmp key> -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=<compose dir>/.ssh/known_hosts"`.
  The key is written to a 0600 temp file for the command's lifetime only.
- `known_hosts` is per Compose and trusts the first host key it sees. A later mismatch fails the
  fetch with ssh's own error. The Source modal's **Reset host key** button deletes the file.
- Only SSH URLs are accepted: `git@host:owner/repo.git` or `ssh://git@host[:port]/owner/repo.git`.
  Any host that supports deploy keys works (GitHub, GitLab, Gitea).
- **Regenerate key** replaces the pair immediately. The old key stops working as soon as it is
  removed on the Git host.
- `git` and `ssh` already ship in `docker:27-cli`. A probe on 2026-10-11 found `/usr/bin/git`,
  `/usr/bin/ssh`, buildx v0.20.1 and Compose v2.33.0, so the panel image needs no new packages.

Fetch, every time:

```
git init (first time) / git remote set-url origin <url>
git fetch --depth 1 origin <branch>
git checkout --force FETCH_HEAD
git clean -ffdx
git submodule update --init --recursive --depth 1   # only when submodules are enabled
```

The Deployment records the commit's SHA and subject. Using `clean -ffdx` is safe because `./src`
belongs to the panel. Nothing else writes there.

### Zip uploads

- Uploads stream to a temp file, with a cap of 500 MiB compressed. Extraction uses `archive/zip` and
  follows the safety rules of `internal/volfs/extract.go`:
  - It refuses absolute names, names that climb out, and any entry type other than a file,
    directory or symlink (see below).
  - It caps the archive at 10,000 entries and 4 GiB uncompressed.
- When every entry sits under one top-level directory (GitHub's "Download ZIP" makes `shop-main/`),
  that directory is stripped.
- Executable bits are kept from each entry's external attributes, so `RUN ./gradlew` and entrypoint
  scripts still run.
- Symlinks are allowed only when they are relative and resolve inside `src/`. Others are refused.
  Git Sources check out whatever the repository holds, and BuildKit never follows a link out of the
  build context anyway, so the rule only keeps an upload from writing outside `src/`.
- Extraction writes into `.src.new/`. Only after it succeeds is `src/` replaced by a rename, so a
  failed upload leaves the previous code in place. An upload replaces the code, it never merges.
- An upload does not deploy. The card then shows "Uploaded, not built yet".
- The upload streams and extracts outside the Project gate, because holding it for a 500 MiB upload
  would block every Compose in the Project. See Concurrency for how it avoids a running Deployment.

### One host-wide queue, concurrency 1

- Every deploy action enqueues a Deployment and returns `202`. One worker goroutine in the panel
  runs the oldest queued Deployment, then the next. Builds and `up` share this one queue, so a long
  build makes the deploy of a Compose without a Source wait. The operator accepted that trade for a host that only ever
  runs one heavy thing at a time.
- A Compose has at most one queued Deployment. Pressing deploy again while one is queued returns
  that Deployment (`200` with the existing number) and does not change its options.
- A Deployment has one of these modes:

  | Mode | Steps |
  |---|---|
  | `deploy` (no Source) | Deploy |
  | `build` (Source) | Fetch → Build `<svc>` … → Deploy |
  | `skip-build` (Source) | Deploy |

  `noCache` adds `--no-cache` to each build. `freshVolumes` runs `down -v` before `up`, as
  "Deploy with fresh volumes" does today.
- **Build from staging files, one Service at a time.** The resolved `docker-compose.yml` and `.env` are
  written only by the Deploy step (`writeFiles` in `internal/composes/composes.go`). Before that step,
  they still hold the last deploy. On a new Compose they do not exist at all. So the Build step first
  writes two staging files into the Compose directory:
  - `.build-compose.yml`: the stored YAML, unresolved.
  - `.build.env`: the merged Environment and Compose Variables, mode 0600, rendered as the Deploy step
    renders `.env`.

  They sit in the same directory so that `./src/...` resolves the same way. The worker then runs
  `docker compose -p <project> -f .build-compose.yml --env-file .build.env build <service>` for each
  Service with `build:`, in YAML order. The project name matches `up`'s, so the image names match.
  The files that stop, restart, logs and the terminal use are not touched until Deploy. The staging
  files are deleted when the Deployment ends. It stops at the first failure. Compose's default build runs
  every Service in parallel, which this design avoids. Two Services with identical `build:` are both
  built, and the second comes almost entirely from BuildKit's cache.
- **`up --no-build` after a build step.** Today `upArgs()` passes `--build`, which would rebuild every
  Service again in parallel. A Deployment with a Source uses `up -d --no-build --remove-orphans`.
  `deploy` mode, with no Source, keeps `--build`, so existing YAML with hand-written `build:` behaves
  as before.
- The Deploy step resolves and writes the files and runs `up` exactly as the synchronous deploy does
  today. It holds the Project gate for this step only. Fetch and build do not take the gate: they
  touch only `./src` and images.
- On success the Deployment stamps `composes.deployed_at`, as the old deploy did.
- **A Deployment uses the state at the moment it starts running**, not when it was enqueued: the
  stored YAML, Variables, Domains, bindings and Source settings. Save & Deploy saves before it
  enqueues, so pressing it always deploys what is on screen, even after a wait in the queue. An edit
  saved while a Deployment waits is included. An edit saved after it starts waits for the next one.

### Cancel, failure, timeout and restart

- Cancel works while queued, fetching or building. It kills the process through its context and
  records `canceled`. During Deploy, the Cancel button is disabled, because killing `up` halfway
  leaves a partly started stack.
- Any failed step stops the Deployment with `failed` and `failed_step` (`fetch`, `build:<service>`,
  `deploy`). Before Deploy, no container is touched.
- A Deployment has a 60-minute timeout, so a stuck build cannot block the host queue forever. A timed
  out Deployment is `failed` with reason `timeout`.
- At startup, any `running` Deployment becomes `failed` with reason `panel restarted`, and the worker
  then resumes the `queued` ones in order.
- Deleting a Compose, its Environment or its Project cancels each affected Deployment that is queued,
  or running in fetch or build, and waits for the process to exit. Deletion holds the Project gate, so
  it must never wait on a Deployment that needs the gate. A Deployment already in its Deploy step
  holds the gate itself, so the delete request simply queues behind it at the gate, and no extra wait
  is added.
- Restart and Stop do not enqueue. They are quick, and the Project gate already keeps them from
  overlapping the Deploy step.

### Concurrency

- The worker is not an HTTP request. It calls the coordinator (`internal/httpapi/coordination.go`)
  directly to acquire the Project gate for the Deploy step.
- Gate exemptions, added to `coordinatedProject` next to the preview one: `POST /source/test`,
  `POST /deployments/{n}/cancel`, `GET /source/dockerfiles`, and the upload's streaming phase.
  Enqueue (`POST /deployments`) and the Source writes take the gate only for their short database
  work.
- **`./src` is never replaced under a running Deployment.** `PUT /source`, `DELETE /source`,
  `POST /source/upload` and Regenerate key return `409`, "A deployment is running", while the Compose
  has a running Deployment. The upload checks before it streams, and again before the rename that
  swaps `src/`. A queued Deployment does not block them, because it reads the Source only when it
  starts.

### Warnings

The warnings the synchronous deploy returns today (Domain label overrides, missing binding targets,
and so on) are written to the log and stored on the Deployment. The UI shows them as an alert above
the log. Preview still shows them before a deploy.

### Build args and secrets

These need no new fields. `build.args` interpolates `${VAR}` from Environment and Compose Variables,
because builds run with the Compose's `--env-file`. A build arg's value is visible in the image's
history. Secret values use Compose build secrets read from the environment:

```yaml
services:
  api:
    build:
      context: ./src/api
      secrets: [npm_token]     # Dockerfile: RUN --mount=type=secret,id=npm_token ...
secrets:
  npm_token:
    environment: NPM_TOKEN     # a Compose Variable, flagged secret
```

The 2026-10-11 probe built this with `docker:27-cli` against the host daemon, using `--env-file`. The
secret reached `RUN --mount=type=secret`, the arg interpolated, and the image was tagged
`<project>-api:latest`.

Rejected: Dokploy's "Create Environment File", which writes a `.env` beside the Dockerfile. It puts
every value inside the build context, and a `COPY . .` bakes them into the image.

The Detect dialog and the Source card link to a short "Build args & secrets" help that can insert
both snippets into the editor draft.

### Dockerfile detection

`GET …/source/dockerfiles` walks `./src` up to depth 4 and skips `.git`, `node_modules` and `vendor`.
It matches `Dockerfile`, `Dockerfile.*` and `*.Dockerfile`, and returns each match's path, a
suggested Service name and a suggested context:

- Each match suggests its directory as the context and that directory's name as the Service name. A
  Dockerfile at the root suggests the Compose's name.
- When two matches suggest the same name, the later ones get `-2`, `-3`.
- For `Dockerfile.<x>` and `<x>.Dockerfile`, the suggested name is `<x>`.

Insert edits the editor draft in the browser and never saves. The operator reviews it with Preview
and saves or deploys as usual. For each selected row:

- A Service that does not exist is added with `build:` and `restart: unless-stopped`.
- A Service that exists gets `build:`, and its `image:` is removed only if the operator ticks
  "replace image".

Comments elsewhere in the draft survive, because the edit is made with the `yaml` package's document
API in the browser, not by re-serialising a parsed object.

## Data model

Migration `00016_sources_and_deployments.sql`. Committed migrations are never edited.

```sql
CREATE TABLE compose_sources (
    compose_id INTEGER PRIMARY KEY REFERENCES composes(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('git', 'zip')),
    repo_url TEXT NOT NULL DEFAULT '',
    branch TEXT NOT NULL DEFAULT '',
    submodules INTEGER NOT NULL DEFAULT 0 CHECK (submodules IN (0, 1)),
    ssh_private_key TEXT NOT NULL DEFAULT '',
    ssh_public_key TEXT NOT NULL DEFAULT '',
    last_test_at INTEGER,
    last_test_ok INTEGER,
    zip_name TEXT NOT NULL DEFAULT '',
    zip_size INTEGER NOT NULL DEFAULT 0,
    zip_files INTEGER NOT NULL DEFAULT 0,
    uploaded_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE deployments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    compose_id INTEGER NOT NULL REFERENCES composes(id) ON DELETE CASCADE,
    number INTEGER NOT NULL,
    mode TEXT NOT NULL CHECK (mode IN ('deploy', 'build', 'skip-build')),
    no_cache INTEGER NOT NULL DEFAULT 0,
    fresh_volumes INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'canceled')),
    step TEXT NOT NULL DEFAULT '',          -- current or last step: fetch, build:<svc>, deploy
    steps TEXT NOT NULL DEFAULT '[]',       -- JSON: planned steps, fixed when the run starts
    failed_step TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',        -- timeout, panel restarted, or the error's first line
    source_ref TEXT NOT NULL DEFAULT '',    -- git SHA, or the zip's name
    source_subject TEXT NOT NULL DEFAULT '',
    warnings TEXT NOT NULL DEFAULT '[]',
    queued_at INTEGER NOT NULL,
    started_at INTEGER,
    finished_at INTEGER,
    UNIQUE (compose_id, number)
);
CREATE INDEX deployments_status_idx ON deployments(status, queued_at);
CREATE UNIQUE INDEX deployments_one_queued ON deployments(compose_id) WHERE status = 'queued';
```

- Logs are files at `<compose dir>/.deployments/<number>.log`. After each Deployment finishes, rows
  and logs beyond the newest 20 for that Compose are deleted.
- `number` is `MAX(number) + 1` for the Compose, assigned in the enqueue transaction.
- Removing a Source deletes the row and `./src`. Images and Deployment history stay.
- Teardown already removes the whole Compose directory, and the cascades remove the rows.

### Validation

| Field | Rule |
|---|---|
| `repo_url` | `git@<host>:<path>.git` or `ssh://git@<host>[:port]/<path>`. No whitespace, no leading `-`, at most 512 chars. |
| `branch` | Empty, or a valid `git check-ref-format --branch` name, at most 255 chars, no leading `-`. |
| zip | `.zip`, at most 500 MiB, plus the extraction limits above. |

Arguments are passed to `exec` as an argv, never through a shell. A leading `-` is refused so that a
value cannot be read as an option.

## API

All paths are under `/api/projects/{p}/environments/{e}/composes/{c}`.

| Method | Path | Body / response |
|---|---|---|
| GET | `/source` | `200 {kind, repoUrl, branch, submodules, publicKey, lastTest, zip{name,size,files,uploadedAt}}`, or `404` when there is none |
| PUT | `/source` | Git only: `{kind:"git", repoUrl, branch, submodules}`. `branch` may be empty, because the key must exist before a test can list branches. On first create it generates the key. `200` with the source. |
| DELETE | `/source` | Removes the row and `./src`. `204`. |
| POST | `/source/key` | Regenerates the key pair. `200 {publicKey}`. |
| POST | `/source/test` | Body `{repoUrl}`, optional. Runs `git ls-remote --heads`. `200 {ok, branches[], error}`; a failed connection is `ok:false`, not an HTTP error. |
| POST | `/source/known-hosts/reset` | `204` |
| POST | `/source/upload` | `multipart/form-data` field `file`. Creates or replaces a zip Source, switching kind from git if needed. `200` with the source. |
| GET | `/source/dockerfiles` | `200 [{path, service, context}]` |
| POST | `/deployments` | `{mode, noCache, freshVolumes}` → `202 {number}`, or `200 {number}` for the already-queued one. `400` for a mode that needs a Source on a Compose without one, or for `build` on a Git Source with no branch. |
| GET | `/deployments` | The newest 20, newest first. |
| GET | `/deployments/{n}` | One Deployment, plus `ahead` (how many queued before it) while queued. |
| GET | `/deployments/{n}/log?offset=N` | `200 {text, offset, done}`. Text from byte N to the current end, with at most 1 MiB per call. |
| POST | `/deployments/{n}/cancel` | `202`, or `409` during Deploy or once finished. |
| GET | `/api/deployments/queue` | Host-wide: `[{composeId, composeName, number, status, step}]` for queued and running. |

`POST /deploy` and `POST /deploy-fresh` are removed. The frontend is their only client.

`POST /deployments`, `/source` writes and `/source/upload` take the Project gate briefly. The GETs,
log reads and `/source/test` do not.

## UI

Use Ant Design at the version in `web/package.json`. Read `https://ant.design/llms.txt` and the
pages it links for each component before building it (AGENTS.md).

**Source card** (Editor tab, right column, below Environment):

- When there is none: one line of help, "Services use images named in the YAML", and **+ Add source**.
- Git: `acme/shop · main`, the connection state from the last test, "Built `a1b2c3d` · subject ·
  1d ago", a "2 new" tag when the branch head (from the last test or fetch) differs, the build
  targets found in the YAML, a **Configure** link and **View deployments →**.
- Zip: file name, size, file count, upload time, "Built from this upload" or "Not built yet",
  **Upload new zip**, **Configure** and **View deployments →**.

**Source modal** (`Segmented`: Git repository / Upload zip):

- Git: SSH URL, saved as soon as it is valid so the key exists, then the public key with a copy button and the hint "Repository → Settings → Deploy
  keys → Add deploy key, leave write access off", then **Test connection**. The Branch `Select` is
  filled from the test's branches. Below it, the Submodules switch and **Save**. A danger area holds
  **Regenerate key**, **Reset host key** and **Remove source**.
- Zip: `Upload.Dragger` with the limits and "Replaces all code in the Source".
- After the first save or upload, the Detect Dockerfiles dialog opens.

**Detect Dockerfiles dialog**: a table with a checkbox, editable Service, Dockerfile, editable
Context, an optional Stage, and "replace image" for existing Services. It has **Insert into editor**
and the "Build args & secrets" link.

**Header**: with a Source, the primary button is **Build & Deploy**. Its dropdown holds Deploy without
building, Build without cache, Deploy with fresh volumes, Restart, Stop and Delete. Without a Source,
it is **Deploy**, and the menu is as today. While the Compose has a queued or running Deployment, the
button shows its state ("Queued · 2 ahead", "Building api") and opens the Deployments tab.

**Deployments tab** (every Compose):

- Left: history of 20, each with number, status icon, source ref and subject, duration and age.
- Right: `Steps` for the selected Deployment, a warnings `Alert`, the reason on failure, and the log
  in a monospace panel that follows the tail.
- While queued, it reads "Waiting — N ahead (shop #14 building)" with **Cancel**.
- The log polls `/log?offset=` every second while the Deployment is running and stops when `done`.
- Starting a deploy switches to this tab.

The redeploy banner and the Domains and Variables tabs' "Deploy now" actions enqueue a Deployment
like the header does.

## Error handling

| Situation | Result |
|---|---|
| Deploy key not added on the Git host | Test: `ok:false`, "Permission denied (publickey)". Fetch: `failed`, `failed_step: fetch`. |
| Host key changed | `failed` at fetch, with ssh's message. The modal offers **Reset host key**. |
| Branch deleted | `failed` at fetch: "couldn't find remote ref". |
| A Dockerfile path or context wrong in YAML | `failed` at `build:<svc>` with Docker's error. Containers untouched. |
| A Source exists but no Service has `build:` | `build` mode runs Fetch, then Deploy, and logs "no Service has build:". |
| Zip unsafe or over the limits | `400` naming the entry or limit. The old `./src` is kept. |
| `skip-build` before any image was built | `failed` at deploy: `up --no-build` reports the missing image. |
| Source created where `src/` exists without the marker | `409`; nothing is written. |
| Source changed or zip uploaded during a running Deployment | `409`, "A deployment is running". |
| Panel restarts mid-run | `failed`, reason `panel restarted`. Queue resumes. |

## Testing

Probes, done 2026-10-11 against `docker:27-cli` and host Docker 29.8.0:

- git and ssh are present in the image.
- A Compose build secret read from the environment reaches `RUN --mount=type=secret` through
  `--env-file`.
- Build args interpolate.
- The image name is `<project>-<service>:latest`.

Go unit tests:

- Key generation, the authorized-key format and the comment.
- The git argv and environment built for a URL, a branch and the submodules setting. Leading `-`
  values are refused.
- Zip extraction:
  - It refuses zip-slip, absolute names, symlinks, and archives over the entry or byte caps.
  - It strips a single top-level directory.
  - It keeps executable bits, and allows only relative symlinks that stay inside `src/`.
  - A failed extraction leaves `src/` unchanged.
- Dockerfile detection: names, contexts, de-duplication, skipped directories and the depth cap.
- The queue, with a fake runner:
  - It runs in FIFO order, one at a time.
  - It de-duplicates per Compose.
  - Cancel works while queued and while building. It is refused during Deploy.
  - It honours the timeout.
  - On restart, `running` becomes `failed` and `queued` is resumed.
  - It keeps 20 per Compose.
- The build steps follow YAML order and stop at the first failure. `up --no-build` with a Source,
  `--build` without.

Go integration (`internal/httpapi`, fake runner):

- The Source endpoints.
- Deployment enqueue and de-duplication.
- Log offsets.
- Cancel `409` during Deploy.
- Delete Compose with a queued Deployment.
- Existing lifecycle tests move to "enqueue, wait for the queue to drain, assert".
- The first Build & Deploy on a brand-new Compose, with `build:` saved just before, builds from the
  staging files.
- Delete Compose while its Deployment is building cancels it and does not deadlock. Delete while it
  is in Deploy waits at the gate and then succeeds.
- Upload and PUT `/source` return `409` while a Deployment runs.

Real git: the fetch path runs against a local bare repository over `file://` (no ssh) and covers the
first clone, a re-fetch after a force-push, a branch switch, and submodules.

Vitest:

- The view-models for the Source card states, the Deployments list and steps, and the queue position
  text.
- The Detect insert edit on a draft with comments.

Manual, on the live install:

- A private GitHub repository with a real deploy key, through key, test, branch, detect and
  Build & Deploy.
- A GitHub "Download ZIP" upload.
- A failing build leaves the old containers running.
- Two Composes deploying at once queue one behind the other.

## Docs to update with the implementation

- `docs/sa.md`: the deploy sequence diagram (`POST /composes/{id}/deploy`) and the use-case table
  become the enqueue, worker and Deployment flow.
- README: a "Building from a Source" section, covering deploy keys, `./src` contexts, build args and
  secrets, and pruning dangling images.

## Follow-ups

- "Prune build cache" and dangling-image prune on the Images page, then auto-prune settings.
- Rollback to an earlier Deployment's image, using the dangling images the panel no longer removes.
- Webhooks for auto-deploy, if ADR-0001's manual-only stance is ever revisited.
