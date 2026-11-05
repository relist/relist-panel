# Multi-user Phase 4 (Compose policy) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Compose whose resolved config could take the host deploys only after an approver approves
that exact config; Editors keep every other power.

**Architecture:** A pure checker, `internal/policy`, reads `docker compose config --format json
--no-env-resolution --profile '*'` output and answers violations plus a hash. `composes.Service`
stages a Deployment snapshot (ADR-0017) to unique scratch files, runs that config through a new
runner call, normalizes the paths that change between runs, and checks it. Approvals live in
`compose_approvals` (migration `00034`). The deployment queue admits a violating snapshot only when its
hash is approved (an approver's deploy with the hash they were shown approves it), and the worker checks
again before the first build and before apply. The Volume Explorer refuses a Volume a violation
names. The Compose page shows violations and an Approve & Deploy button.

**Tech Stack:** Go 1.25, SQLite + goose, `docker compose` v5 CLI; React 19, antd 6.5.1, SWR, vitest.

**Spec:** `.scratch/multi-user/spec.md` (Compose policy, `compose_approvals`, UI → Compose page, Phases 4,
Testing, Docs).

## Global Constraints

- Migration is `internal/migrations/00034_compose_approvals.sql`; never edit a committed migration; database tests count 34.
- `RELIST_POLICY_APPROVER` is `admins` (default, also when empty) or `owner`; anything else stops the panel at startup. `RELIST_POLICY_BIND_ROOTS` is comma-separated absolute paths, cleaned; a relative one stops startup. Both are read from the environment only.
- The approver is any admin (`admins`) or only users.id 1 (`owner`).
- The check runs on `docker compose config --format json --no-env-resolution` with every profile enabled (`--profile '*'`, verified on v5.5.1).
- Violation messages name the service, the key and the path only; never an environment or label value.
- `configHash` reaches only approvers (it is a hash over interpolated secrets).
- An error while evaluating refuses; never passes.
- Restart and stop are not checked. Composes already running are untouched until their next deploy.
- Every new route is classified in `internal/authz` (tests fail otherwise); `POST …/approval` stays out of `Routes` (admin-only) and the handler refuses a non-Owner admin under `owner`. Action `compose.approve`; an approving Deployment's `deployment.create` detail carries `approved: true`.
- Conventional Commits, no scope, with the Co-Authored-By trailer.

## Review Focus

1. Secret leakage: `configHash` or a violation message reaching a Viewer/Operator; the 409 body to a non-approver carries violations, never the hash.
2. Build before up: `build.privileged`/`network: host`/entitlements act during the build steps; the worker must check before the first build, and again at apply (fresh-volume destroy, rollouts, up).
3. Hash stability: GET policy, enqueue and worker give the same hash for the same saved config across Deployments (private Source copy and staging paths normalized, JSON re-marshalled); a variable value change changes it.
4. Allowlist completeness against real v5.5.1 output: `use_api_socket`, `provider`, `gpus`, hook `privileged`, `cgroup: host`, mount types other than bind/volume/tmpfs, `driver_opts` named volumes in the Volume Explorer, and the panel's own `.ssh` (a Source's private key) inside the Compose directory.
5. A git Source in build mode at enqueue: the private copy is empty, so `config` can fail on files under `src/`; that error alone does not refuse at enqueue (the worker check after fetch decides), but a successful evaluation with violations does.

## Rulings made while planning

1. The check runs on what deploys (the snapshot, panel keys injected), not the YAML before injection: it then checks exactly what `up` runs. Injected keys (Domain labels, external `relist-network`, log cap, sets `env_file`, zero-downtime labels) must pass; the ordinary-Compose fixtures include them. Cost if wrong: a false violation on an injected key, caught by those fixtures.
2. Inside the Compose directory a bind may not be the directory itself nor `.ssh` or anything in it (`.ssh` holds a Source's git key from the admins' SSH key library).
3. `include` and `extends` are checked in the stored YAML before `config` runs: each path must be relative, without `..` and without `$`; a remote include (URL) is a violation. Includes inside Source files are merged and their content checked, their own paths are not (ADR note).
4. Roots the panel container cannot see (a `RELIST_POLICY_BIND_ROOTS` entry not mounted into it) are compared as text only; symlinks there are not resolved (ADR note).
5. The Compose list tag is left out: an accurate one needs a `docker compose config` per Compose per list load. The Compose page shows the state.
6. Saving a Compose is unchanged; the page reads violations from `GET …/policy` after a save.
7. A new git commit changes the hash, so an approved violating Compose with a git Source needs approving again.

---

### Task 1: The checker

**Files:** Create `internal/policy/{policy.go,check.go,paths.go,env.go}` and tests.

**Interfaces:** Produces:
- `type Violation struct { Service string \`json:"service"\`; Message string \`json:"message"\` }`
- `type Rules struct { ComposeDir string; Roots []string; ProjectName string; PanelDomain string }`
- `func Check(config []byte, stored string, r Rules) ([]Violation, error)` — config is the JSON above, stored the Compose's YAML as saved.
- `func Hash(config []byte) (string, error)` — SHA-256 hex of the re-marshalled JSON.
- `type Approver string` (`ApproverAdmins`, `ApproverOwner`); `func ParseApprover(string) (Approver, error)`; `func ParseRoots(string) ([]string, error)`; `func (a Approver) Allows(userID int64, isAdmin bool) bool`.

- [ ] Tests (`check_test.go`) over JSON fixtures, one per spec violation: bind outside roots (long and short form; `configs`/`secrets`/`build.secrets` `file:`), symlink escaping a root (temp dir), nested binds, `.ssh` and the Compose dir itself, `env_file`/`label_file` outside, secret/config `environment:`, `build.ssh`, named volume `driver_opts`/`driver`/`external`/foreign `name`, `volumes_from` outside, each of `privileged cap_add devices device_cgroup_rules security_opt sysctls cgroup_parent runtime use_api_socket gpus provider`, `userns_mode: host`, `cgroup/pid/ipc/uts/network_mode` host or foreign `container:`/`service:`, hook `privileged`, mount types other than bind/volume/tmpfs, `build.context`/`dockerfile`/`additional_contexts` outside, `build.network: host`, `build.privileged`, `build.entitlements`, unknown service key, `include`/`extends` path rules in the stored YAML, `traefik.*` label naming the panel domain. One ordinary fixture (named volume, `./data` bind, Domain labels, `relist-network` external, logging, sets `env_file`, healthcheck, deploy resources) has no violations. `Hash` equal for reordered keys.
- [ ] Implement; allowlist of service keys taken from the ordinary fixture plus Compose's documented harmless keys.
- [ ] Env tests: `ParseApprover("")`/`admins`/`owner`/`bad`; `ParseRoots("/srv, /data/")` → `[/srv /data]`, relative → error. `config.Load` returns an error for a bad approver (test in `internal/config`). Add `Policy{Approver, Roots}` to `config.Config`; pass both through `docker-compose.yml`.
- [ ] Commit `feat: check a resolved compose config against the host policy`.

### Task 2: The runner call

**Files:** `internal/composes/composes.go` (interface), `internal/dockerexec/dockerexec.go`, every fake `Config` implementer.

**Interfaces:** `PolicyConfig(ctx context.Context, req RunRequest) ([]byte, error)` on `ComposeRunner`: argv `compose -p … -f … --env-file … --profile * config --format json --no-env-resolution`. Fakes return their `Config` output.

- [ ] argv test in dockerexec; implement; fakes compile.
- [ ] `internal/policy/docker_test.go` (skips without `docker`): each Task 1 violation written in YAML with its value behind `${VAR}` from an env file fails `Check`; the ordinary Compose passes; changing a variable value changes `Hash`.
- [ ] Commit `feat: resolve a compose config for the policy with every profile and env files unread`.

### Task 3: Evaluation and approvals

**Files:** Create `internal/migrations/00034_compose_approvals.sql`, `internal/composes/policy.go`; test files.

**Interfaces:**
- table per spec (`compose_id` PK, `config_hash`, `approved_by`, `approved_at`).
- `type PolicyResult struct { Violations []policy.Violation; Hash string; Approved bool }`
- `func (s *Service) SetPolicy(rules func(t DeployTarget) policy.Rules)`; `func (s *Service) EvaluatePolicy(ctx, t DeployTarget) (PolicyResult, error)` — t must carry a Snapshot (Capture it when nil).
- `func (s *Service) Approve(ctx, composeID int64, hash string, userID int64) error`.

Evaluation stages the snapshot with `stageSnapshot` at unique `.policy-<rand>` paths in the Compose directory, runs `PolicyConfig`, removes the staging, rewrites string values in the JSON — the private Source copy `<dir>/<snapshots>/<n>/src…` → `<dir>/src…`, the staged env file → `<dir>/.env`, the staged sets dir → `<dir>/sets` — then `Check` and `Hash`, and reads the approval.

- [ ] Tests: hash equal for a Target captured twice and for a snapshot whose YAML points at two different private copies; approval matches only that hash; database count 34.
- [ ] Commit `feat: evaluate a compose's snapshot against the policy and keep approvals`.

### Task 4: Policy and approval routes

**Files:** `internal/composes/policy_http.go`, `internal/authz/{routes.go,actions.go}`, `internal/auth` status (`isApprover`), `internal/httpapi/httpapi.go` wiring.

- `GET …/composes/{composeID}/policy` (Viewer) → `{violations, configHash (approvers only, else null), approved}`.
- `POST …/composes/{composeID}/approval {configHash}` (admin route; handler 403 when the approver is `owner` and the user is not id 1) → 409 `{error, policy}` when the hash differs, else 204 and `compose.approve`.
- [ ] httpapi tests: Viewer gets violations without hash; approve with stale hash 409; approve then GET shows approved; owner mode refuses admin id 2; route classification tests pass.
- [ ] Commit `feat: show a compose's policy and let an approver approve it`.

### Task 5: Admission at enqueue

**Files:** `internal/deployments/{queue.go,http.go}`.

**Interfaces:** `Request` gains `Approver bool; ConfigHash string`; Deps gains `Policy interface{ EvaluatePolicy; Approve }` (composes.Service). `type PolicyError struct{ Result composes.PolicyResult; Stale bool }` → 409 `{error, policy: {violations, approvalRequired: true, configHash?}}`.

Enqueue evaluates the captured snapshot. Passing or approved → queue. Violating: an approver with `ConfigHash == hash` approves then queues (`audit.Detail approved true`); otherwise 409. An evaluation error is a refusal, except a `config` error with a git Source in build mode (Review Focus 5), which queues for the worker to decide.

- [ ] httpapi tests with the fake runner returning a violating config: non-approver 409 without hash; approver without/with stale hash 409; approver with hash 202 and approval stored; approved hash lets a non-approver deploy.
- [ ] Commit `feat: refuse to queue a violating compose until an approver approves it`.

### Task 6: Worker check

**Files:** `internal/deployments/queue.go`.

Before the first build in `runPrepare` and at the start of `runApply`: evaluate `j.target`; violating and not approved, or an error → step fails `"policy: …"` naming the violations.

- [ ] queue tests: an approval removed after queueing fails the Deployment before build; a passing config deploys.
- [ ] Commit `fix: check the policy again before a deployment builds or starts`.

### Task 7: Volume Explorer

**Files:** `internal/volumeapi/operations.go`, wiring.

A Volume opens when no violation names it (bind source equal or inside, or volume key), or when the user is an approver and the current config is approved; else 403 `This volume is outside what the policy allows`.

- [ ] Test: saved `/etc:/x` bind → 403 for a non-approver Editor; approver with approval → passes.
- [ ] Commit `fix: keep the volume explorer out of binds the policy refuses`.

### Task 8: Compose page

**Files:** `web/src/data/api.ts`, `web/src/pages/ComposePage.tsx` (or its deploy component), status `isApprover`.

Alert "This Compose needs approval" listing violations; non-approver Deploy disabled reading "Waiting for approval"; approver sees "Approve & Deploy" sending `configHash`, and an Approve button. Read antd llms.txt first.

- [ ] vitest tests for both roles; rebuild `internal/webassets/dist`.
- [ ] Commits `feat: show policy violations and approval on the compose page`, `chore: rebuild the embedded frontend`.

### Task 9: Docs

ADR-0032 Compose policy; CONTEXT Compose policy and Approval; README variables; spec comment "2026-11-05 — Phase 4 rulings".

- [ ] Commit `docs: record the compose policy in ADR-0032, the domain and the README`.
