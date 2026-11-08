# Audit IP is the client address Traefik forwards

_Amends the "never a forwarded header" rule of `.scratch/multi-user/plan-3-audit.md`._

Audit events recorded the host of `r.RemoteAddr`. The panel is reached through Traefik, so that was always Traefik's address on the Docker network, never the client's.

**Decision:** `internal/clientip.From` returns the leftmost `X-Forwarded-For` entry when it parses as an IP address (a port is dropped), and the host of `r.RemoteAddr` otherwise. Login, failed login, invite and authorization events all use it.

The header is trusted without a peer allow-list because Traefik has already applied the operator's Trusted proxies (ADR-0027, ADR-0030): it replaces `X-Forwarded-For` from an untrusted peer with that peer's address and keeps it from a trusted one such as Cloudflare. The panel would only repeat that decision with a second list that could drift from the first.

## Considered options

**Trust only a peer inside the `relist-network` subnet.** Rejected: it needs the panel to discover the subnet and still fails the same way behind a second proxy.

**Keep the TCP peer.** Rejected: the audit log then names no client.

## Consequences

- A caller that reaches the panel without Traefik can write any address into the audit log. That is `127.0.0.1:<RELIST_PORT>` on the host and any container on `relist-network`; both already have more access than an audit IP can grant.
- Rows written before this change keep Traefik's address.
