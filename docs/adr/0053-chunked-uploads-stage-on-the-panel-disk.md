# Chunked uploads stage on the panel's disk

A panel behind a Cloudflare proxy cannot take a large file in one request. Free and Pro zones refuse bodies over 100 MB, no plan accepts 10 GiB, and the origin must answer each request within 125 s. Until now, every file upload streamed one request body straight into its consumer and stored nothing: the volume helper, `docker load`, or the source unzip. The image-upload spec made that a stated rule: no staging on the panel's disk.

**Decision:** large uploads go through `internal/chunkupload`. The client sends the file in chunks of 32 MiB, and no request body may exceed 64 MiB. Each chunk is appended at a declared `Upload-Offset` to one staged file under `<data>/.uploads/`, and its progress is kept in the `uploads` table. Once every byte has arrived, a background job streams the staged file into the same consumer a direct upload uses, and the client polls for the result. The staged file is deleted when the consumer succeeds, kept on failure so the user can retry, and swept after 24 hours without activity. Volume upload is the first user, with a 10 GiB cap (`.scratch/chunked-upload/spec.md`). The direct endpoints stay for small files and for curl.

For chunked uploads, this replaces the no-staging rule. ADR-0010 still holds: consuming the staged file is one `docker run --rm` of the helper.

## Considered options

**Raise the single-request caps.** Rejected: no Cloudflare plan carries a 10 GiB body, and a write that runs for minutes inside one request meets the 125 s read timeout.

**A DNS-only (grey cloud) hostname for uploads.** Rejected: it exposes the origin's IP, which the proxy exists to hide.

**Feed chunks into a consumer process held open across requests.** No disk cost. Rejected: an upload could not survive a panel restart, chunks would have to reach a live pipe strictly in order, and an abandoned upload would leave a process to reap.

**tus 1.0 through tusd and tus-js-client.** Rejected: two new dependencies for protocol surface the panel does not use, and tusd owns its routes, which does not fit `authz.Routes`. The protocol borrows tus's offset model instead.

## Consequences

- An open upload holds disk space equal to the file on the data directory's disk, which SQLite shares. Starting an upload is refused unless 2 GiB stays free after the space every open upload still needs.
- On a one-disk host, writing the staged file into a Volume briefly needs twice the file's size: the staged copy and the helper's temp file. Starting an upload therefore asks the helper for the Volume's free space and refuses below twice the size, so a file that cannot fit is refused before any byte is sent, not after.
- Requests under `…/volumes/uploads` take neither the Project's mutation gate nor its volume lock. The consume job takes both itself, for as long as the helper runs. Once it holds them it checks that its upload still exists: a Compose delete that held the locks first has removed it, and `docker run -v` would otherwise recreate the deleted volume to write into it.
- Chunk requests are not written to the Audit log. `authz.Unrecorded` lists them, and starting, completing and cancelling an upload are recorded.
- `.uploads` is left out of a server migration. Uploads that were open when the server moved must be started again on the new host.
