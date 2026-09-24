# 09 — Overview

**What to build:** The design's Overview screen (GUIDELINES §4.3): the four stat cards, the "needs
attention" list derived from the composes, the all-composes table, and `Refresh statuses`. Needs a
flat "all composes" read — the mock loops over Projects and Environments, and so can this, unless an
endpoint is added. The Activity card is deliberately omitted (§11: nothing is recorded).

**Blocked by: 01, 02, 03, 04, 06**

**Status:** ready-for-agent

- [ ] Stats match the design's definitions (Composes = stacks, Containers = running/total, attention =
      partly running or stopped-after-success)
- [ ] The needs-attention list opens the Compose it names, one row per degrade
- [ ] The all-composes table sorts and links; "Last edited" and "Last deployed" are relative times
- [ ] `Refresh statuses` re-reads container status and shows its own loading state
- [ ] Empty state when the panel has no Projects yet, as designed

## Comments
