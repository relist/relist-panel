# Audit IP is the client Traefik saw, trusted only from Traefik

Audit events recorded the host of `r.RemoteAddr`, "never a forwarded header", to match the Access log's `ClientAddr` (ADR-0027). The panel is reached through Traefik, so every Audit event named Traefik's address on relist-network and never the client. The failed-login limiter, keyed by that IP, also put every client in one bucket.

**Decision:**

- `internal/clientip` resolves the address once per request, in the outermost middleware, and `clientip.From` hands it to every Audit event: sign-in, failed sign-in, invite and authorization.
- `X-Forwarded-For` is read **only when the TCP peer is the install's Traefik**: the address `docker inspect` gives for the install project's `traefik` service on relist-network, the lookup the Traefik page already uses. Any other peer is recorded as is: a Container on relist-network that reaches `panel:8080` directly, or an SSH tunnel to the loopback port.
- Traefik's address changes whenever Traefik is recreated (a Traefik apply, a Panel update). A peer that does not match triggers a new lookup, **at most one every 10 s**, so traffic from other Containers cannot keep the panel asking Docker. A found address is **checked again after 5 minutes** even while it keeps matching, since Docker may give Traefik's old address to another Container.
- The client is the **rightmost `X-Forwarded-For` entry that is not in Traefik's own Trusted proxies**, read from the effective static config (ADR-0030). Traefik v3.7 drops `X-Forwarded-*` and `X-Real-Ip` from an untrusted peer and then appends its peer, so the last entry is always the address Traefik saw. When that is a trusted proxy (Cloudflare), the entry left of it is what the proxy saw, and so on. An entry that does not parse stops the walk at the last trusted proxy. An empty header (`notAppendXForwardedFor`) falls back to `X-Real-Ip`, which Traefik sets to its peer.
- The address is stored in the existing `ip` column. The peer is not kept beside it: when the header is read, the peer is always Traefik.

## Considered options

**Leftmost `X-Forwarded-For` from any peer** (closed PR #70). Rejected for two reasons. User Composes join relist-network by default, so any deployed app could reach the panel directly and write any address into the Audit log. And behind Cloudflare, Traefik keeps the client's own header, so the leftmost entry is whatever the client typed.

**Trust any peer in a private or Docker range.** Rejected: it trusts the same relist-network Containers.

**Find Traefik by DNS (`relist-traefik`).** Rejected: a Compose service can claim that name as a network alias, and Docker's DNS would then answer with its address too.

**Store both the client and the peer, as the Access log keeps `ClientHost` and `ClientAddr`.** Rejected by the operator: the peer is Traefik whenever the header is used, so a second column would add a migration without telling the reader anything.

## Consequences

- Rows written before this change keep Traefik's address.
- After Traefik is recreated, a request can record Traefik's new address when a lookup ran in the previous 10 s.
- A Container that gets Traefik's old address can have its forged header believed for up to 5 minutes after the last lookup. Docker hands out freed addresses one by one, so this needs Traefik recreated and a new Container started on relist-network inside that window.
- A panel without a Traefik (development, a broken install) records the TCP peer and logs the lookup error once, until the error changes.
- Someone who can change Traefik's Trusted proxies (an Admin) decides which proxies may name the client. That matches what Traefik itself does with them.
