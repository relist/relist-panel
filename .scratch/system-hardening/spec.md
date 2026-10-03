# System review fixes

Status: closed

Source: system review at `8c924a6`; user authorized all eight fixes on 2026-10-03.

1. New Composes use their immutable database ID for Docker identity and deploy directory. Existing rows retain their frozen slugs and paths; migration adds identity version 0 without moving files or changing stacks. Ambiguous legacy identities refuse operations before Docker runs. The API returns actual deployment identity for UI displays and confirmations. See ADR-0011.
2. Deleting a Project or Environment tears down child containers/networks/volumes and deployment files first. A failed teardown preserves metadata and reports the affected Compose; already completed child teardowns are not rolled back. Retry continues safely. Every deletion entry (Projects list, Project Settings and Environment Settings) discloses container/volume/file removal and partial failure.
3. Environment Variable values survive dotenv parsing literally, including dollars, quotes, whitespace and multiline content. Reject NUL at create/update because container environments cannot represent it.
4. Coordinate requests within each Project across lifecycle mutations, parent deletion, child creation and Volume operations. Different Projects remain independent. A cancelled waiting request must not run; release gates on errors too. Project granularity deliberately keeps parent/child ordering simple for one admin.
5. Restore validates gzip through EOF, including checksum/footer, with bounded trailing decompression under the remaining uncompressed budget. Failures retain partial extracted files.
6. Explorer and direct backup require a mounting Service or valid numeric uid:gid override. UID 0 requires visible explicit acknowledgement; unverifiable binds require separate acknowledgement. Forward identity and acknowledgements through every operation URL; enforce them before mounting at the API too.
7. File Save records the submitted snapshot as original and preserves text typed while the request is running. Later text remains dirty. Editor sessions and pending read contexts are guarded so navigation, identity changes and reopening a same-named file cannot apply old content to a new editor.
8. Direct backup from the Volumes tab uses the same root/bind acknowledgement requirements before starting the browser download.

Tests use the existing public HTTP seam with Docker fakes, exported filesystem operations and frontend decision/API helpers. The dotenv regression also uses the real Docker Compose parser without creating containers.

## Verification

- Full `go test ./...` and `go vet ./...` passed.
- Race detector passed lifecycle coordination, identity isolation and teardown retry tests.
- Frontend typecheck and 241 tests passed; production Vite build passed.
- Docker image built successfully. Its helper wrote/read literal bytes, archived/restored a disposable Volume, and refused a corrupt gzip CRC with exit 4. The test Volume was removed.
- Headless Chrome with mocked local API verified no reads before choosing an identity, independent root/bind acknowledgement gates, access parameters on downloads, draft B remaining dirty after saving snapshot A, actual configured deploy paths and destructive deletion disclosures in both Settings and Projects list, and zero runtime exceptions. Screenshots were inspected.
- Final Standards and Spec reviews found no actionable issues after fixing pending editor reads, omitted API identities, deployment identity display and every parent-deletion confirmation.

Real Docker parent teardown is covered through the existing fake runner HTTP seam; no existing deployed stacks were changed during validation.
