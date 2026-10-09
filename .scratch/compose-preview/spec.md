# Spec — Compose preview (show the file a deploy would write)

Date: 2026-10-09. Builds on the Domains feature (branch `feat-domains`).
Status: ready-for-agent

## Problem

A deploy changes the operator's YAML before `docker compose` sees it:

- It joins every Service to `relist-network` and adds the generated `env_file` (ADR-0002).
- Since Domains, it also adds Traefik labels and overrides some hand-written ones, with warnings.

None of this can be seen until after a deploy, and only by reading the file on the host.

## Behaviour

- The Compose editor card has a **Preview** button. It opens a read-only modal with the
  resolved `docker-compose.yml` that a deploy would write for the **editor's current
  draft**, including unsaved edits (a deploy saves them first anyway). It also shows the
  warnings the deploy would return.
- The preview is exactly `resolveCompose` output. `${VAR}` references are not interpolated,
  and no environment variable value appears anywhere in the response. Keys come back
  re-sorted and comments are dropped, as in the deployed file. The modal says so.
- Errors that would fail a deploy before Docker runs show inline in the modal, not as a toast:
  a blank draft, invalid YAML, or a domain whose Service the draft no longer declares.
- Nothing is written to disk and Docker is never called. The deploy-identity check is skipped:
  it concerns the project name, not the file.

## API

`POST /api/projects/{p}/environments/{e}/composes/{c}/preview`, body `{"yamlContent": string}`
(required, same 1 MiB cap as update). Response `200 {"yaml": string, "warnings": string[]}`.
`400` for a blank or invalid draft or a domain target error, and `404` for an unknown Compose.

Preview is read-only, so the project gate (`internal/httpapi/coordination.go`) exempts it. A
preview must not queue behind a running deploy.
