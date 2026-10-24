# 10 — A Domain cannot take over the panel's own host

**What to build:** Traefik picks the longest rule by default. A Domain with HTTPS on (or an explicit
`websecure` entrypoint) whose host is `RELIST_DOMAIN` and whose path is, say, `/api` outranks the
panel router: the SPA loads but every API call reaches the app, and the operator cannot sign in to
undo it. Verified on Traefik v3.7.

**Blocked by:** none

**Status:** closed

- [ ] `docker-compose.yml`: `traefik.http.routers.panel.priority` and
      `traefik.http.routers.panel-http.priority` set high (e.g. 100000); README notes the existing
      install must recreate the panel container to pick up the labels
- [ ] Domain create/update refuses a host equal to the install's `RELIST_DOMAIN`
      (`config.InstallSettings.Domain`; empty means unknown and refuses nothing)
- [ ] Tests: validation refuses the panel host on create and update, case-insensitively

## Source

Stability review, item C3. Traefik reproduction:
`.scratch/stability-review/verification/c3-traefik-dynamic.yml`.
