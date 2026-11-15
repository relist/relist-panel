package dbquery

import (
	"regexp"
	"strings"
)

// A quoted literal after PASSWORD, IDENTIFIED BY or IDENTIFIED WITH <word> BY.
// PASSWORD('x') (the MySQL function) is covered by the optional paren.
var sqlSecret = regexp.MustCompile(`(?is)(\b(?:password\s*\(?|identified\s+by|identified\s+with\s+\w+\s+by)\s*)('(?:[^'\\]|\\.|'')*'|"(?:[^"\\]|\\.|"")*")`)

// RedactSQL replaces the quoted password literals with '***'.
func RedactSQL(s string) string {
	return sqlSecret.ReplaceAllString(s, "${1}'***'")
}

// RedactRedis masks what a command line holds secret.
func RedactRedis(args []string) []string {
	out := append([]string(nil), args...)
	if len(out) == 0 {
		return out
	}
	mask := func(from, to int) {
		for i := from; i < to && i < len(out); i++ {
			out[i] = "***"
		}
	}
	switch strings.ToUpper(out[0]) {
	case "AUTH":
		mask(1, len(out))
	case "CONFIG":
		if len(out) > 1 && strings.EqualFold(out[1], "SET") {
			for i := 2; i+1 < len(out); i++ {
				if k := strings.ToLower(out[i]); k == "requirepass" || k == "masterauth" {
					out[i+1] = "***"
				}
			}
		}
	case "ACL":
		if len(out) > 1 && strings.EqualFold(out[1], "SETUSER") {
			for i := 3; i < len(out); i++ {
				if strings.HasPrefix(out[i], ">") || strings.HasPrefix(out[i], "#") {
					out[i] = "***"
				}
			}
		}
	case "MIGRATE":
		for i := 1; i < len(out); i++ {
			switch strings.ToUpper(out[i]) {
			case "AUTH":
				mask(i+1, i+2)
			case "AUTH2":
				mask(i+1, i+3)
			}
		}
	}
	return out
}
