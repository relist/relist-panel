# A host-wide SSH key library

ADR-0016 gave each Git Source its own **Deploy key**, generated when the Source was first saved. The reason was that Git hosts refuse one deploy key on two repositories. An operator who reaches several repositories through one machine-user key could not reuse it: every Compose got a fresh pair to add to its repository, and no page listed the keys or where they were used.

**Decision:** SSH keys live in a host-wide library. The operator generates them on the SSH keys page, and a Git Source chooses one. One key may serve many Composes. This supersedes ADR-0016's "per-Compose Deploy key the panel generates".

- **Keys are generated only in the library.** ed25519, named, with the comment `relist-<name>`. The Source modal only selects. Rejected: importing a private key, regenerating in place, other key types and passphrases. To rotate, generate a new key and switch the Sources to it.
- **The panel does not police reuse.** Whether one key goes on several repositories is the operator's concern on the Git host: a machine-user key works across repositories.
- **Deleting a key detaches it.** Sources that used it keep their URL and branch but have no key, show a warning and cannot build until one is chosen. Their last connection test is cleared.
- **A queued Deployment keeps its key** (ADR-0017), so deleting a key does not break Deployments already in the queue.

## Consequences

- New `ssh_keys` table and `compose_sources.ssh_key_id`. Existing per-Source keys are discarded: Git Sources have no key after the upgrade. The old `ssh_private_key` and `ssh_public_key` columns stay, unused, because dropping them in SQLite rebuilds the table.
- `POST .../source/key` (regenerate) is removed. Queuing a `build` Deployment for a Git Source without a key is refused with `400`.
- The private key is stored in the database and never returned by the API. Like ADR-0016's Deploy key, this is not a security boundary: an Admin owns the host.
- A Migration (ADR-0018) copies `panel.db` whole, so the library travels with no change.
- Spec: `.scratch/ssh-keys/spec.md`.
