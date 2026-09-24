# 08 — Variables, reshaped

**What to build:** The Variables tab per the design (GUIDELINES §4.5): values masked by default with
reveal and copy, the `secret` / `plain` tag, key filter, create/edit/delete, and the bulk `.env`
paste with its live parse preview, duplicate-key error and "replace N variables with M" confirmation.
Replaces the monolith's `EnvironmentVariablesPanel`.

**Blocked by: 01, 02, 03, 04, 06**

**Status:** ready-for-agent

- [ ] Masking, reveal and copy work per row; the `secret` tag renders as designed
      (the API has no flag yet — §11 — so the tag is client-side for now, and the ticket notes that)
- [ ] Key validation matches the server's `[A-Za-z_][A-Za-z0-9_]*`
- [ ] Bulk paste parses, previews, refuses duplicates, and confirms before replacing
- [ ] Delete uses the design's confirmation level (Popconfirm for a single variable)
- [ ] No feature lost against the monolith's variables panel

## Comments
