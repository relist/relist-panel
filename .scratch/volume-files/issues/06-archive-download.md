# 06 — Archive download

**What to build:** Downloading a directory as tar.gz or zip, streamed end to end with nothing staged
on disk.

**Blocked by:** 01, 05

**Status:** wontfix

- [ ] `GET` serves a directory as `tar.gz` or `zip` chosen by a `format` parameter; a single file is served as a plain download, not wrapped in an archive
- [ ] The stream runs helper stdout → exec → HTTP response with no temp file anywhere, so a 50 GB Volume never becomes a 50 GB file in the data directory
- [ ] Entry names are set explicitly to the path relative to the Volume root: slash-separated, never absolute, never containing a `..` segment. `tar.FileInfoHeader` supplies only the base name, so it is always overwritten
- [ ] Symlinks are stored as symlinks; hardlinks are written as regular files, which sidesteps the hardlink tar-slip vector entirely
- [ ] `Content-Disposition` follows RFC 6266: control characters stripped, `filename*` percent-encoded for non-ASCII names
- [ ] The Explorer can report before the download how many symlinks the archive will contain and how many of them point outside the Volume, so the extraction-time hazard is disclosed rather than hidden
- [ ] Tests produce an archive containing a nested path, a newline-bearing name, an inside-root symlink and an outside-root symlink; read it back with `archive/tar` and `archive/zip`; assert every entry name is relative and `..`-free and that both symlinks survived as symlinks

## Comments

Closed `wontfix` by the re-plan: this merged into 05. The archive is tar.gz only and
streamed by the one download endpoint, so there is nothing left for a separate ticket to
own.
