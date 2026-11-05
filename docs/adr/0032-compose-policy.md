# A Compose that could take the host deploys only once an approver approves it

ADR-0031 gave Editors the Compose file. With nothing else in the way, an Editor could deploy a Compose that mounts the Docker socket or `/`, runs privileged, or opens another Project's volume, then use the Terminal: Editor meant root on the host.

**Decision:** Every Deployment's config is checked against the **Compose policy** before it queues, again after its Source is fetched and before the first build, and again just before `up`. The check reads `docker compose config --format json --no-env-resolution` with every profile enabled (`--profile '*'`), on the exact snapshot the Deployment freezes (ADR-0017), so interpolation, `include`, `extends` and profiles cannot hide a value; `include` and `extends` paths are checked in the saved YAML too, since `config` merges them away. The rules are an **allowlist**: a service key the checker does not know is a violation. A config that violates deploys only when `compose_approvals` (migration `00034`) holds an **Approval** for its hash, the SHA-256 of the resolved config with its keys in a fixed order. An approver approves on the Compose page, or by deploying with the hash they were shown; a stale hash answers 409, so an edit saved between looking and clicking is never approved unseen. The hash changes with any change to the resolved config, a variable value or a new git commit included.

The approver is every Admin (`RELIST_POLICY_APPROVER=admins`, the default) or only the Owner (`owner`). Host paths outside a Compose's directory that it may bind without approval are listed in `RELIST_POLICY_BIND_ROOTS`. Both are read from the environment only, so a stolen Admin session cannot widen them; a bad value stops the panel at startup.

What is refused (internal/policy): a bind, `env_file`, `label_file`, config or secret file outside the Compose directory and the bind roots, after symlinks; the Compose directory itself and its `.ssh` (a Source's git key); a bind inside another bind; secrets and configs read from the panel's environment; named volumes with `driver_opts`, another driver, `external`, or a name other than `<project>_<key>`; `volumes_from`, `network_mode`, `pid`, `ipc`, `uts` or `cgroup` reaching the host or a container outside the Compose; `privileged`, `cap_add`, `devices`, `device_cgroup_rules`, `security_opt`, `sysctls`, `cgroup_parent`, `runtime`, `gpus`, `use_api_socket`, `provider`, `userns_mode: host`, privileged hooks, mounts other than bind, volume and tmpfs; build contexts, Dockerfiles and additional contexts outside the Compose directory, host build network, privileged builds, entitlements and build SSH; a `traefik.*` label naming the panel's own domain.

The Volume Explorer checks the deployed file it resolves from: a Volume a violation names opens only for an approver once the current config is approved.

## Considered options

**Checking the YAML before the panel injects its keys.** The spec's wording. Rejected for what deploys: the snapshot is what `up` runs, and the panel's own keys (Domain labels, the shared network, the log cap, Variable Set files) pass the rules.

**A denylist.** Rejected: Compose grows keys (`use_api_socket`, `provider`) faster than a denylist would follow.

**Showing the hash to everyone.** Rejected: it is a hash over interpolated secret values, which a Viewer could test guesses against. Only approvers get it.

## Consequences

- **Admins remain fully trusted.** An Admin who is not an approver still uses the host-wide pages (Docker host, Migration serve hands out `panel.db`).
- **Composes already running are untouched**; their next deploy is checked. Restart and stop are not checked.
- **What it does not stop:** a kernel exploit from an ordinary container; a symlink race across Deployments (a container from the previous Deployment that can write a bound directory could swap a path between the check and `up`; the nesting rule closes it within one config and the last check runs just before `up`); symlinks under a bind root the panel container cannot see, which are compared as text; files a Source's own `include` reaches, whose content is checked but whose paths are not.
- A git Source in build mode cannot be resolved before it is fetched; such a Deployment queues and the check after the fetch decides.
- The Compose list shows no policy tag: an accurate one would run `docker compose config` per Compose per load.
- Spec: `.scratch/multi-user/spec.md`. Plan: `.scratch/multi-user/plan-4-policy.md`.
