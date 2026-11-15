package dbquery

import (
	"regexp"
	"strings"
)

// The literal that holds a secret: quoted (with an E or N prefix), or
// dollar-quoted without a tag. What comes before it names a password:
// PASSWORD (also "PASSWORD FOR user =" and the function PASSWORD(...)),
// IDENTIFIED BY / WITH x BY|AS / VIA x USING, and REPLACE, which carries the
// old password.
const sqlLiteral = `[eEnN]?(?:'(?:[^'\\]|\\.|'')*'|"(?:[^"\\]|\\.|"")*"|\$\$.*?\$\$)`

var sqlSecret = regexp.MustCompile(`(?is)(\b(?:password(?:\s+for\s+[^=;]+?\s*=|\s*=?)\s*\(?|identified\s+(?:by(?:\s+password)?|(?:with|via)\s+\w+\s+(?:by|as|using)(?:\s+password)?)\s*\(?|replace)\s*)` + sqlLiteral)

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
	case "HELLO":
		for i := 1; i < len(out); i++ {
			if strings.EqualFold(out[i], "AUTH") {
				mask(i+1, i+3)
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
