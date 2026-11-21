// Package dbquery runs the Query panel's statements against a Database over
// relist-network and shapes what comes back: engine runners, the MySQL
// batch splitter, result caps and password redaction.
package dbquery

import (
	"fmt"
	"strings"
	"unicode"
)

// Statement is one statement of a batch.
type Statement struct {
	Text  string
	Start int // char (rune) offset of its first non-space char in the input
}

// SplitMySQL splits a batch the way the mysql client does: a delimiter
// (";" until a DELIMITER line changes it) ends a statement outside quotes,
// backticks and comments. Comments of the "--" and "#" kinds stay in the
// text; "/* */" is dropped unless it is a "/*! */" executable comment.
// Statements holding nothing but comments are dropped. The error names the
// line where an unterminated quote or comment starts.
func SplitMySQL(sql string) ([]Statement, error) {
	rs := []rune(sql)
	delim := []rune{';'}
	var out []Statement
	var buf []rune
	start, content := -1, false
	lineStart := true // only whitespace since the last newline

	flush := func() {
		text := strings.TrimSpace(string(buf))
		if content && text != "" {
			out = append(out, Statement{Text: text, Start: start})
		}
		buf, start, content = nil, -1, false
	}
	lineOf := func(i int) int { return 1 + strings.Count(string(rs[:i]), "\n") }
	hasPrefix := func(i int, p []rune) bool {
		if i+len(p) > len(rs) {
			return false
		}
		for k, r := range p {
			if rs[i+k] != r {
				return false
			}
		}
		return true
	}
	put := func(i int, r rune, real bool) {
		if start < 0 && !unicode.IsSpace(r) {
			start = i
		}
		buf = append(buf, r)
		if real && !unicode.IsSpace(r) {
			content = true
		}
	}

	for i := 0; i < len(rs); {
		r := rs[i]
		if lineStart && strings.TrimSpace(string(buf)) == "" && !unicode.IsSpace(r) {
			// A DELIMITER command line, only between statements.
			j := i
			for j < len(rs) && rs[j] != '\n' {
				j++
			}
			fields := strings.Fields(string(rs[i:j]))
			if len(fields) > 0 && strings.EqualFold(fields[0], "DELIMITER") {
				if len(fields) < 2 {
					return nil, fmt.Errorf("DELIMITER on line %d needs a delimiter", lineOf(i))
				}
				delim = []rune(fields[1])
				buf = nil
				i = j
				continue
			}
		}
		if r == '\n' {
			lineStart = true
		} else if !unicode.IsSpace(r) {
			lineStart = false
		}
		switch {
		case hasPrefix(i, delim):
			flush()
			i += len(delim)
			lineStart = false
		case r == '\'' || r == '"' || r == '`':
			begin := i
			put(i, r, true)
			i++
			closed := false
			for i < len(rs) {
				c := rs[i]
				put(i, c, true)
				i++
				if c == '\\' && r != '`' && i < len(rs) {
					put(i, rs[i], true)
					i++
					continue
				}
				if c == r {
					if i < len(rs) && rs[i] == r { // doubled quote
						put(i, rs[i], true)
						i++
						continue
					}
					closed = true
					break
				}
			}
			if !closed {
				return nil, fmt.Errorf("unterminated quote starting on line %d", lineOf(begin))
			}
		case r == '#' || (r == '-' && hasPrefix(i, []rune("--")) && (i+2 >= len(rs) || rs[i+2] == ' ' || rs[i+2] == '\t' || rs[i+2] == '\n' || rs[i+2] == '\r')):
			for i < len(rs) && rs[i] != '\n' {
				put(i, rs[i], false)
				i++
			}
		case r == '/' && hasPrefix(i, []rune("/*")):
			begin := i
			keep := i+2 < len(rs) && rs[i+2] == '!'
			end := -1
			for j := i + 2; j+1 < len(rs); j++ {
				if rs[j] == '*' && rs[j+1] == '/' {
					end = j + 2
					break
				}
			}
			if end < 0 {
				return nil, fmt.Errorf("unterminated comment starting on line %d", lineOf(begin))
			}
			if keep {
				for ; i < end; i++ {
					put(i, rs[i], true)
				}
			} else {
				i = end
			}
		default:
			put(i, r, true)
			i++
		}
	}
	flush()
	return out, nil
}
