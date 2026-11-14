# Web assets are precompressed at build and cached as immutable

The panel served its frontend through `http.FileServer` over `embed.FS`, with no compression and no cache validators: embedded files have no modtime, so there was no `Last-Modified`, no `ETag` and no `Cache-Control`. Every load or refresh downloaded about 7.5 MB again, 6.1 MB of it the main bundle. On production the Monitoring page showed panel p50 at 73 ms but p90/p99 at 5.8–6.9 s. The panel runs with `cpus: 0.25`, and its `cpu.stat` showed throttling in only about 1.6% of periods, so the time went into transfer, not CPU.

**Decision:**

- The Vite build writes a `.br` (quality 11) and a `.gz` (level 9) next to every compressible file in `assets/` of at least 1 KiB, keeping only siblings that are smaller than the original.
- `internal/webassets` hashes every embedded file once at start and sends the brotli sibling, then the gzip one, when `Accept-Encoding` names that coding without `q=0`. Otherwise it sends the original. A response with a sibling carries `Vary: Accept-Encoding` and its own ETag (`<hash>-br`, `<hash>-gzip`). A sibling is used only when the extension's content type is known, because a browser cannot sniff compressed bytes.
- Files under `assets/` get `Cache-Control: public, max-age=31536000, immutable`, since Vite puts a content hash in their names. Everything else, the shell above all, gets `no-cache` with an ETag, so a Panel update shows on the next load and an unchanged shell answers 304.
- `.br`/`.gz` names and directories are not served by their own path. They fall through to the shell like any unknown path.

## Considered options

**Compress per request in the panel** (gzip middleware). Rejected: the panel has a quarter of a CPU, and compressing 6 MB on each miss would use the CPU quota and throttle API requests running at the same time.

**Traefik's `compress` middleware.** Rejected: it also compresses on every request (Traefik has a full CPU, but it would still be repeated work), it needs the panel's router labels in the Install file, and it gives no cache headers.

**Ship only the compressed files and decompress for clients that send no `Accept-Encoding`.** Rejected: it would save about 7 MB of binary but add a decompress path for clients that almost never exist.

## Consequences

- The binary grows by about 3.3 MB for the siblings. `pnpm build` takes about 7 s longer for brotli quality 11.
- A first load is about 1.6 MB brotli instead of 7.5 MB. A repeat load fetches only the shell, or gets a 304 for it. Splitting Monaco out of the main bundle is a separate change.
- A Range request on a compressed response counts bytes of the encoded body, as the HTTP spec defines.
