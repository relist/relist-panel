# 09 — Stop

**What to build:** An operator can stop a deployed Compose's containers without deleting its saved YAML.

**Blocked by:** 08

**Status:** ready-for-agent

- [ ] `dockerexec.Down` (without volumes) is implemented for real and added to the fake `ComposeRunner`
- [ ] Clicking "Stop" (per `ComposeGeneral.dc.html`) brings the Compose's containers down for real
- [ ] The status badge reflects "Stopped" afterward
- [ ] Covered by HTTP-seam tests using the fake `ComposeRunner`
