package dbquery

import (
	"fmt"
	"strings"
	"unicode"
)

// isIdentRune is a rune that can sit inside an unquoted identifier.
func isIdentRune(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// SplitPostgres splits a batch the way the Postgres lexer reads it: ";"
// outside '…' and E'…' strings, "…" identifiers, $tag$…$tag$ bodies and
// comments ends a statement. Comments stay in the text; "--" runs to the
// line's end, and "/* */" nests. Statements holding only comments or
// whitespace are dropped. Start counts runes, as in SplitMySQL. The error
// names the line where an unterminated quote, dollar body or comment starts.
func SplitPostgres(sql string) ([]Statement, error) {
	rs := []rune(sql)
	var out []Statement
	var buf []rune
	start, content := -1, false
	flush := func() {
		text := strings.TrimSpace(string(buf))
		if content && text != "" {
			out = append(out, Statement{Text: text, Start: start})
		}
		buf, start, content = nil, -1, false
	}
	lineOf := func(i int) int { return 1 + strings.Count(string(rs[:i]), "\n") }
	put := func(i int, r rune, real bool) {
		if start < 0 && !unicode.IsSpace(r) {
			start = i
		}
		buf = append(buf, r)
		if real && !unicode.IsSpace(r) {
			content = true
		}
	}
	// dollarTag reads the tag of a dollar quote opening at i (rs[i] == '$'):
	// empty, or an identifier not starting with a digit. $1 is a parameter.
	dollarTag := func(i int) (string, bool) {
		j := i + 1
		for j < len(rs) && isIdentRune(rs[j]) && rs[j] != '$' {
			j++
		}
		if j >= len(rs) || rs[j] != '$' {
			return "", false
		}
		tag := rs[i+1 : j]
		if len(tag) > 0 && unicode.IsDigit(tag[0]) {
			return "", false
		}
		return string(tag), true
	}
	hasPrefix := func(i int, p string) bool {
		pr := []rune(p)
		if i+len(pr) > len(rs) {
			return false
		}
		for k, r := range pr {
			if rs[i+k] != r {
				return false
			}
		}
		return true
	}
	for i := 0; i < len(rs); {
		r := rs[i]
		switch {
		case r == ';':
			flush()
			i++
		case r == '\'' || r == '"':
			begin := i
			// E'…' takes backslash escapes; '…' and "…" only double.
			esc := r == '\'' && i > 0 && (rs[i-1] == 'E' || rs[i-1] == 'e') && (i < 2 || !isIdentRune(rs[i-2]))
			put(i, r, true)
			i++
			closed := false
			for i < len(rs) {
				c := rs[i]
				put(i, c, true)
				i++
				if esc && c == '\\' && i < len(rs) {
					put(i, rs[i], true)
					i++
					continue
				}
				if c == r {
					if i < len(rs) && rs[i] == r { // a doubled quote
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
		case r == '$' && (i == 0 || !isIdentRune(rs[i-1])):
			// A $ right after an identifier rune is part of that identifier
			// (a$b$c is one name), never a dollar-quote opener.
			tag, ok := dollarTag(i)
			if !ok {
				put(i, r, true)
				i++
				continue
			}
			begin := i
			closer := "$" + tag + "$"
			for k := 0; k < len([]rune(closer)); k++ {
				put(i, rs[i], true)
				i++
			}
			found := -1
			for j := i; j < len(rs); j++ {
				if hasPrefix(j, closer) {
					found = j
					break
				}
			}
			if found < 0 {
				return nil, fmt.Errorf("unterminated quote starting on line %d", lineOf(begin))
			}
			for end := found + len([]rune(closer)); i < end; i++ {
				put(i, rs[i], true)
			}
		case r == '-' && hasPrefix(i, "--"):
			for i < len(rs) && rs[i] != '\n' {
				put(i, rs[i], false)
				i++
			}
		case r == '/' && hasPrefix(i, "/*"):
			begin := i
			depth, j := 0, i
			for j < len(rs) {
				if hasPrefix(j, "/*") {
					depth++
					j += 2
					continue
				}
				if hasPrefix(j, "*/") {
					depth--
					j += 2
					if depth == 0 {
						break
					}
					continue
				}
				j++
			}
			if depth != 0 {
				return nil, fmt.Errorf("unterminated comment starting on line %d", lineOf(begin))
			}
			for ; i < j; i++ {
				put(i, rs[i], false)
			}
		default:
			put(i, r, true)
			i++
		}
	}
	flush()
	return out, nil
}
