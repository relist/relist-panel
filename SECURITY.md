# Security

## Reporting a vulnerability

Report it privately through GitHub: on this repository's **Security** tab, choose
**Report a vulnerability**. Please do not open a public issue for it.

Include what you found, how to reproduce it, and the version (`relist-panel version`
or the image tag). This is a personal project maintained in spare time: a report is
read, but there is no promised response time.

Only the latest release gets security fixes.

## What the panel can do to its host

Relist Panel mounts the Docker socket (`/var/run/docker.sock`). Anyone who can use
the panel, or who takes over its Container, can do anything Docker can do on the
host, which is the same as root. Treat the panel's sign-in as root access.

What the panel does about it:

- One admin account, created only from a shell on the host (`create-admin`). There
  is no sign-up page.
- Two-factor sign-in (TOTP), and a sign-in lock after repeated failed attempts.
- Session cookies are `HttpOnly`, `SameSite=Lax`, and `Secure` over HTTPS; requests
  from another origin are refused.
- The panel's own port listens on `127.0.0.1` only. From outside the host it is
  reached through Traefik over HTTPS, or through an SSH tunnel.

What you should do:

- Turn on two-factor sign-in and use a long password.
- If you can, keep the panel off the public internet: reach it over a VPN or an SSH
  tunnel (`ssh -L 8888:127.0.0.1:8888 <host>`), and route only your Services
  through Traefik.
- Run only Compose files and Sources you trust. A Compose can mount host paths and
  run privileged Containers; the panel deploys what you give it.
- Keep the panel updated (Panel update, or `install.sh --update`).
