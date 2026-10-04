# 09 — Caps and the free-space check

**What to build:** The hard limits that keep this feature from filling a small VPS. Fixed values, no
env knobs — they exist to protect the host this panel is sized for, and a knob is an invitation to
turn them off.

**Blocked by:** 01, 08

**Status:** wontfix

- [ ] An upload above 1 GB is refused
- [ ] An upload larger than the free space on the Volume's filesystem is refused, with the helper checking `statfs`. This is the part no OS default provides, and a full disk is a host-wide failure rather than a panel one
- [ ] The free-space and size checks reject early — before the body is streamed to the helper where it can be
- [ ] A text file above 1 MB is not opened in the editor; the Explorer says so instead. Monaco is not usable at that size anyway
- [ ] An archive stream is cancelled after 10 minutes and the handle's in-flight operation released
- [ ] HTTP-seam tests for each refusal, asserting the message names the limit it hit

## Comments

Closed `wontfix` by the re-plan: the caps it described live in the tickets whose
operations enforce them — upload and free space in 08, restore in 12.
