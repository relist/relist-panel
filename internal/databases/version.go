package databases

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
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

// dbSettingsSQL lists a database's ALTER ROLE … IN DATABASE … SET and ALTER
// DATABASE … SET rows, one setconfig element per line: the role (empty for a
// database-wide setting) and the element, each hex-encoded so no value can
// break psql's lines or fields. A one-database pg_dump does not carry these
// rows and DROP DATABASE deletes them; settingsSQL turns them back into
// statements. The server never builds SQL text from them: a value a role
// stored with SET … FROM CURRENT is its raw session text, unquoted.
const dbSettingsSQL = `SELECT CASE WHEN s.setrole = 0 THEN '' ELSE encode(convert_to(r.rolname::text, 'UTF8'), 'hex') END,
  encode(convert_to(c.cfg, 'UTF8'), 'hex')
FROM pg_catalog.pg_db_role_setting s
JOIN pg_catalog.pg_database d ON d.oid = s.setdatabase
LEFT JOIN pg_catalog.pg_roles r ON r.oid = s.setrole
CROSS JOIN LATERAL unnest(s.setconfig) WITH ORDINALITY AS c(cfg, n)
WHERE d.datname = :name AND (s.setrole = 0 OR r.rolname IS NOT NULL)
ORDER BY s.setrole <> 0, r.rolname, c.n;`

// dbSettingsQuery is dbSettingsSQL for one database.
func dbSettingsQuery(name string) string {
	return strings.Replace(dbSettingsSQL, ":name", pgLit(name), 1)
}

// listSettings are the settings whose value is a list of names, which the
// server keeps as a comma-separated list of identifiers: pg_dump's
// variable_is_guc_list_quote.
var listSettings = map[string]bool{
	"local_preload_libraries": true, "search_path": true, "session_preload_libraries": true,
	"shared_preload_libraries": true, "temp_tablespaces": true, "unix_socket_directories": true,
}

// settingsSQL turns dbSettingsQuery's output for database db into the
// statements that recreate the settings. Every name is an identifier and
// every value a literal: a list setting is split the way pg_dump splits it
// and each element is its own literal, so "$user", public stays two names.
// A list that does not split goes back as one literal.
func settingsSQL(db, out string) (string, error) {
	var b strings.Builder
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		roleHex, cfgHex, ok := strings.Cut(line, "|")
		if !ok {
			return "", fmt.Errorf("unexpected settings line %q", line)
		}
		role, err1 := hex.DecodeString(roleHex)
		cfg, err2 := hex.DecodeString(cfgHex)
		if err := errors.Join(err1, err2); err != nil {
			return "", fmt.Errorf("unexpected settings line %q: %w", line, err)
		}
		name, value, ok := strings.Cut(string(cfg), "=")
		if !ok || name == "" {
			return "", fmt.Errorf("unexpected setting %q", cfg)
		}
		lit := pgLit(value)
		if listSettings[name] {
			if names, ok := splitGUCList(value); ok {
				quoted := make([]string, len(names))
				for i, n := range names {
					quoted[i] = pgLit(n)
				}
				if lit = strings.Join(quoted, ", "); lit == "" {
					lit = "''"
				}
			}
		}
		if len(role) == 0 {
			fmt.Fprintf(&b, "ALTER DATABASE %s SET %s TO %s;\n", pgIdent(db), pgIdent(name), lit)
		} else {
			fmt.Fprintf(&b, "ALTER ROLE %s IN DATABASE %s SET %s TO %s;\n", pgIdent(string(role)), pgIdent(db), pgIdent(name), lit)
		}
	}
	return b.String(), nil
}

// roleSettingsSQL lists the role-wide settings (ALTER ROLE … SET, in no
// database), hex-encoded like dbSettingsSQL. pg_dumpall --roles-only
// carries these, and Change version replays its output.
const roleSettingsSQL = `SELECT encode(convert_to(r.rolname::text, 'UTF8'), 'hex'), encode(convert_to(c.cfg, 'UTF8'), 'hex')
FROM pg_catalog.pg_db_role_setting s
JOIN pg_catalog.pg_roles r ON r.oid = s.setrole
CROSS JOIN LATERAL unnest(s.setconfig) WITH ORDINALITY AS c(cfg, n)
WHERE s.setdatabase = 0
ORDER BY r.rolname, c.n;`

// ErrUnsplittableSetting refuses a Postgres Change version: a role-wide list
// setting does not split the way pg_dump splits it (a role can store any
// raw text with SET … FROM CURRENT), so pg_dumpall writes "SET … TO ;" and
// restoring the roles on the new version would fail.
type ErrUnsplittableSetting struct{ Role, Setting string }

func (e ErrUnsplittableSetting) Error() string {
	return fmt.Sprintf("role %s has a %s setting that pg_dumpall cannot copy, so Change version would fail and roll back. "+
		"Run ALTER ROLE %s RESET %s in the Query tab, then try again", e.Role, e.Setting, pgIdent(e.Role), e.Setting)
}

// unsplittableRoleSetting reads roleSettingsSQL's output and returns the
// first role-wide list setting pg_dumpall cannot write back, or nil.
func unsplittableRoleSetting(out string) (*ErrUnsplittableSetting, error) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		roleHex, cfgHex, ok := strings.Cut(line, "|")
		if !ok {
			return nil, fmt.Errorf("unexpected role settings line %q", line)
		}
		role, err1 := hex.DecodeString(roleHex)
		cfg, err2 := hex.DecodeString(cfgHex)
		if err := errors.Join(err1, err2); err != nil {
			return nil, fmt.Errorf("unexpected role settings line %q: %w", line, err)
		}
		name, value, _ := strings.Cut(string(cfg), "=")
		if !listSettings[strings.ToLower(name)] {
			continue
		}
		if _, ok := splitGUCList(value); !ok {
			return &ErrUnsplittableSetting{Role: string(role), Setting: name}, nil
		}
	}
	return nil, nil
}

// checkRoleSettings refuses a Postgres Change version whose roles dump would
// not restore (ErrUnsplittableSetting). It runs before anything changes.
func (s *Service) checkRoleSettings(ctx context.Context, d Database, dp Dumper) error {
	out, detail, err := s.admin(ctx, d, dp, roleSettingsSQL)
	if err != nil {
		return fmt.Errorf("read the role settings: %w: %s", err, strings.TrimSpace(redactPasswords(detail)))
	}
	bad, err := unsplittableRoleSetting(out)
	if err != nil {
		return err
	}
	if bad != nil {
		return *bad
	}
	return nil
}

// splitGUCList is pg_dump's SplitGUCList: names separated by commas, each
// either double-quoted (with "" for a quote) or running to a comma or
// whitespace. Names are not downcased. ok is false on bad syntax.
func splitGUCList(s string) (names []string, ok bool) {
	isSpace := func(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f' }
	i := 0
	skip := func() {
		for i < len(s) && isSpace(s[i]) {
			i++
		}
	}
	skip()
	if i == len(s) {
		return []string{}, true
	}
	for {
		// A separator with no name after it: pg_dump refuses an empty name.
		if i == len(s) {
			return nil, false
		}
		var name strings.Builder
		if s[i] == '"' {
			i++
			for {
				j := strings.IndexByte(s[i:], '"')
				if j < 0 {
					return nil, false
				}
				name.WriteString(s[i : i+j])
				i += j + 1
				if i < len(s) && s[i] == '"' {
					name.WriteByte('"')
					i++
					continue
				}
				break
			}
		} else {
			start := i
			for i < len(s) && s[i] != ',' && !isSpace(s[i]) {
				i++
			}
			if i == start {
				return nil, false
			}
			name.WriteString(s[start:i])
		}
		names = append(names, name.String())
		skip()
		if i == len(s) {
			return names, true
		}
		if s[i] != ',' {
			return nil, false
		}
		i++
		skip()
	}
}
