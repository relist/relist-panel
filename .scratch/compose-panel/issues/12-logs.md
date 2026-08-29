# 12 — Logs

**What to build:** An operator can view a deployed Compose's recent logs on demand.

**Blocked by:** 08

**Status:** ready-for-agent

- [ ] `ComposeRunner.Logs` is implemented for real (`docker compose logs --tail=200`) and added to the fake
- [ ] The Logs tab (per `ComposeLogs.dc.html`) fetches and displays the last ~200 lines on demand, with a manual refresh button — no live streaming
- [ ] Covered by HTTP-seam tests using the fake `ComposeRunner`
