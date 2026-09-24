# 06 — Compose Monitor tab

**What to build:** Port `web-design/src/components/ServiceMonitor.tsx` and the `Editor | Monitor` tab
row of the Compose screen: a service picker fed by the Services endpoint, the window control, the
facts strip (image, container ID, state, PIDs, last deploy, and the line explaining why history is
keyed by Service), and the four per-service charts (CPU on an auto axis because a container can exceed
one core, memory against the container's own limit, network I/O, block I/O) with the deploy marker.
The status rail also gains its per-Service rows from the same endpoint.

**Blocked by: 04 (this feature), and 07 of the design port (the Compose screen)**

**Status:** open

- [ ] The service picker lists the Compose's Services with their state; selecting one changes every
      number on the screen
- [ ] The deploy marker appears on all four charts when the last deploy falls inside the window, and
      is absent when it does not
- [ ] A Compose that has never been deployed shows the empty state pointing at the Editor tab
- [ ] The rail's per-Service rows show name, state, health, image and ports from the same endpoint
- [ ] Verified as in 05, plus a real-input interaction test for the picker (a synthetic click does not
      open antd v6's Select — drive it through CDP input events)

## Comments
