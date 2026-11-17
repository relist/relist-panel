package databases

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// compareTags orders two version tags by their dot-separated numbers; a
// missing part reads as 0, so "8.4" equals "8.4.0".
func compareTags(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// HigherVersions lists the engine's offered tags above current, lowest
// first: the versions Change version may move to. Never nil.
func HigherVersions(engine, current string) []string {
	out := []string{}
	e, ok := Engines[engine]
	if !ok {
		return out
	}
	for _, v := range e.Versions() {
		if compareTags(v.Tag, current) > 0 {
			out = append(out, v.Tag)
		}
	}
	sort.Slice(out, func(i, j int) bool { return compareTags(out[i], out[j]) < 0 })
	return out
}

// filterRoles drops postgres's own role from a pg_dumpall --roles-only dump:
// the new server already has it, with the Database's password. Every other
// line, \restrict included, passes through unchanged.
func filterRoles(dump string) string {
	lines := strings.Split(dump, "\n")
	out := lines[:0]
	for _, l := range lines {
		if l == "CREATE ROLE postgres;" || strings.HasPrefix(l, "ALTER ROLE postgres WITH ") {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// psql cuts a long LINE echo with "...", so the closing quote may be
// missing: then the literal runs to the end of the line.
var passwordLiteral = regexp.MustCompile(`(?i)(PASSWORD\s+)'(?:[^'\n]|'')*'?`)

// redactPasswords hides every PASSWORD '…' literal: psql echoes a failing
// statement, and a roles dump's statements carry the SCRAM hashes.
func redactPasswords(s string) string {
	return passwordLiteral.ReplaceAllString(s, "${1}'***'")
}

// dbSettingsSQL prints, one per line, the statements that recreate a
// database's ALTER ROLE … IN DATABASE … SET and ALTER DATABASE … SET rows. A
// one-database pg_dump does not carry them and DROP DATABASE deletes them.
// List settings such as search_path are stored already quoted and go in
// as they are, like pg_dump does; every other value is a literal.
const dbSettingsSQL = `SELECT CASE WHEN s.setrole = 0
    THEN format('ALTER DATABASE %I SET %s TO %s;', d.datname, quote_ident(split_part(c.cfg, '=', 1)), c.val)
    ELSE format('ALTER ROLE %I IN DATABASE %I SET %s TO %s;', r.rolname, d.datname, quote_ident(split_part(c.cfg, '=', 1)), c.val)
  END
FROM pg_db_role_setting s
JOIN pg_database d ON d.oid = s.setdatabase
LEFT JOIN pg_roles r ON r.oid = s.setrole
CROSS JOIN LATERAL (
  SELECT cfg, CASE WHEN split_part(cfg, '=', 1) IN ('search_path', 'temp_tablespaces', 'session_preload_libraries', 'local_preload_libraries')
    THEN substr(cfg, strpos(cfg, '=') + 1)
    ELSE quote_literal(substr(cfg, strpos(cfg, '=') + 1)) END AS val
  FROM unnest(s.setconfig) AS cfg
) c
WHERE d.datname = :name
ORDER BY 1;`

// dbSettingsQuery is dbSettingsSQL for one database.
func dbSettingsQuery(name string) string {
	return strings.Replace(dbSettingsSQL, ":name", pgLit(name), 1)
}
