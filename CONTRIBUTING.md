# Contributing

Relist Panel is a personal project. Issues and pull requests are welcome, but
there is no promise that either gets a reply or a review, or how soon.

## Issues

- **Bugs:** use the bug report template. Say which version you run and include the
  panel's log (`docker logs relist-panel`).
- **Security problems:** do not open an issue; see [SECURITY.md](SECURITY.md).
- **Ideas:** open an issue before writing code, so you know whether it fits. The
  panel stays small on purpose: one operator, one Docker host, Compose files only
  (see the scope in [CONTEXT.md](CONTEXT.md)).

## Pull requests

- Keep one change per pull request, with tests for it.
- Commit messages follow Conventional Commits without a scope: `feat: ...`,
  `fix: ...`, `docs: ...`, `chore: ...`.
- Never edit a migration that is already on `main`; add a new one.
- A change to how the panel works may need an ADR in [docs/adr](docs/adr) and the
  words in [CONTEXT.md](CONTEXT.md).
- Frontend changes follow [docs/GUIDELINES.md](docs/GUIDELINES.md) and Ant Design's
  components.

## Documentation

[docs/README.md](docs/README.md) indexes the docs by system, with one guide page per
system under `docs/guide/` (the first is [Traefik](docs/guide/traefik.md)). A change to
a system updates its guide page. If the system has no page yet, add one with the seven
sections the index lists, and link it from the index.

## Build and test

You need Go (see `go.mod`), Node.js with pnpm, and [go-task](https://taskfile.dev).

```sh
pnpm --dir web install
task build        # the embedded frontend and the binary
task test         # the Go tests
task typecheck    # the frontend types
pnpm --dir web test
```

The built frontend in `internal/webassets/dist/` is not committed: every checkout (and
every worktree) builds its own with `task build-web`, and the Dockerfile builds it for
images. Do not commit it; a binary built without it answers every page with a 503 that
says to run `task build-web`.

Run the binary with its own data directory, never the one of a real install:

```sh
RELIST_DATA_DIR="$PWD/.data" ./relist-panel
```

The probes under `scripts/` (`task probe:panel-update`, `task probe:migration`)
run real Docker inside `docker:dind` and need privileged Containers.

## Working with AI agents

Most of this repo was written with AI coding agents. [AGENTS.md](AGENTS.md) and
[docs/agents](docs/agents) hold the instructions they follow; they apply to
human contributors too.

## Code of conduct

This project follows the [Code of Conduct](CODE_OF_CONDUCT.md).
