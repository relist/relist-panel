# 05 — Auth screens

**What to build:** Setup and sign-in per the design (GUIDELINES §4.7): a single centred card, the
panel's wordmark, the setup flow's password rules and confirm field, and the design's error handling.
Replaces the monolith's `AuthScreen`.

**Blocked by: 01, 02, 03, 04**

**Status:** ready-for-agent

- [ ] `/setup` and `/signin` match the design; setup is reachable only while the API reports
      `setupRequired`
- [ ] Field-level validation matches the design's copy; a wrong password renders the design's error
      state, not a toast
- [ ] Signing out returns to `/signin` and the shell is gone
- [ ] No feature lost against the monolith's auth flow (setup, login, logout, error surfacing)

## Comments
