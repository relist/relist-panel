# 10 — Restart

**What to build:** An operator can restart a deployed Compose's containers without a full redeploy.

**Blocked by:** 08

**Status:** ready-for-agent

- [ ] `dockerexec.Restart` is implemented for real and added to the fake `ComposeRunner`
- [ ] Clicking "Restart" (per `ComposeGeneral.dc.html`) cycles the Compose's containers for real
- [ ] Covered by HTTP-seam tests using the fake `ComposeRunner`
