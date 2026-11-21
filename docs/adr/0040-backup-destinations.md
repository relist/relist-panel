# Backup destinations: a host-wide library of S3-compatible buckets

Scheduled Backups only lived on the host's own disk, so losing the host lost them too (`.scratch/databases/spec.md`, phase 3). The panel now copies scheduled Backups to off-site storage the Admin configures once.

**Decision:** A **Backup destination** is a named, host-wide entry on Data → Backup destinations, like SSH keys and External registries. Admin only.
- **Type.** Phase 3 ships one `type`, `s3`: endpoint, region, bucket, prefix, access key ID, secret access key and a path-style toggle. That covers AWS S3, Cloudflare R2, Backblaze B2 and MinIO. Other types (SFTP, WebDAV) are later `type`s of the same table (`.scratch/databases/spec-2.md`).
- **Secrets.** The secret is stored in the panel database (`backup_destinations.secret`, migration `00047`) and never leaves it: no response, log, error or audit detail carries it. Editing leaves it blank to keep it. A kept secret stays with its endpoint: changing the endpoint asks for it again, as External registries do.
- **Client library.** `github.com/minio/minio-go/v7` (18 modules), not `aws-sdk-go-v2` (about 130 packages), for a much smaller dependency footprint.
- **Object key.** `<prefix>/<db slug>-<db id>/<logical database>/<file name>`, with the logical database in the key so a person browsing the bucket can tell the files apart.
- **Uploads.** After a scheduled Backup succeeds, its file is uploaded with its size, and the object's size is checked afterwards. A failed upload leaves the local Backup successful and offers **Retry upload**. An upload that makes no progress for 5 minutes is cut off; a remote Delete and a Test each have a 1-minute timeout, so a hung bucket can never block the host-wide scheduler.
- **Remote deletion only touches what the panel recorded uploading** (`database_backup_uploads`). It never lists the prefix and deletes what it finds. Deleting a Backup, or a Database with its Backups, deletes the remote copies first; if one cannot be deleted, nothing is deleted and the error names the destination, so no copy is left untracked.
- **Deleting a destination.** Refused while a schedule uses it (the error names the Databases). A destination that only holds copies can be deleted after a confirmation stating how many; the copies stay in the bucket and the panel stops tracking them.
- **Remote only.** Local Rotation may delete a file whose copy is still tracked; the Backup stays listed as Remote only and cannot be downloaded or restored from the panel.
- **No restore from a destination** in phase 3: download the object from the bucket and use Restore from file.

## Considered options

**Per-Database bucket settings.** Rejected: the same credentials would be typed and stored once per Database, and rotating a key would mean editing every one.

**Listing the prefix to find copies to rotate or delete.** Rejected: a shared or misconfigured prefix would let the panel delete objects it never wrote.

## Consequences

- The panel holds long-lived bucket credentials; anyone with the panel database has them.
- Copies are not encrypted by the panel in phase 3 (encryption is planned in `spec-2.md`, phase 9b).
