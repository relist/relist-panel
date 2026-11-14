package databases

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// ErrForbiddenStatement is a line of an upload that would leave its target or
// touch users.
type ErrForbiddenStatement struct {
	Line int
	Text string // the line, cut to 80 bytes
}

func (e ErrForbiddenStatement) Error() string {
	return fmt.Sprintf("line %d: %q is not allowed in an upload; dump one database with pg_dump <db> or mysqldump <db>", e.Line, e.Text)
}

var (
	// forbiddenStatement is a statement that creates, drops or alters a
	// database, a role or a user. IF [NOT] EXISTS is allowed before the
	// object kind, as the plan words it, and after it, as SQL does.
	forbiddenStatement = regexp.MustCompile(`^(CREATE|ALTER|DROP)\s+(OR\s+REPLACE\s+)?(IF\s+(NOT\s+)?EXISTS\s+)?(DATABASE|ROLE|USER)\b`)
	userMapping        = regexp.MustCompile(`^(CREATE|ALTER|DROP)\s+USER\s+MAPPING\b`) // a foreign-data object, not a user
	mysqlOnly          = regexp.MustCompile(`^(USE\s|(CREATE|DROP|ALTER)\s+SCHEMA\b)`)
	mysqlVersioned     = regexp.MustCompile(`^/\*M?!\d*\s*`) // /*!40000 ... */ and MariaDB's /*M!100100 ... */
	setPassword        = regexp.MustCompile(`^SET\s+PASSWORD\b`)
)

// maxLineKept is how much of a line CheckUpload looks at; a data line can be
// many megabytes, and every rule is about its first words.
const maxLineKept = 4096

// CheckUpload reads plain SQL and refuses the first line that would leave the
// target database or change users, naming its 1-based line number. The caller
// un-gzips r.
func CheckUpload(engine string, r io.Reader) error {
	br := bufio.NewReaderSize(r, 64*1024)
	line, inCopy := 0, false
	for {
		frag, more, err := br.ReadLine()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		line++
		if len(frag) > maxLineKept {
			frag = frag[:maxLineKept]
		}
		text := string(frag)
		for more { // drop the rest of a long line
			if _, more, err = br.ReadLine(); err != nil {
				return err
			}
		}
		if inCopy {
			if text == `\.` {
				inCopy = false
			}
			continue
		}
		if forbidden(engine, text, &inCopy) {
			return ErrForbiddenStatement{Line: line, Text: cut(strings.TrimSpace(text), 80)}
		}
	}
}

// forbidden reports whether a line is refused. For postgres it also sets
// inCopy when the line opens a COPY data block.
func forbidden(engine, text string, inCopy *bool) bool {
	t := strings.TrimLeft(text, " \t")
	if engine == "postgres" {
		if strings.HasPrefix(t, `\`) {
			return !strings.HasPrefix(t, `\restrict`) && !strings.HasPrefix(t, `\unrestrict`)
		}
		up := strings.ToUpper(strings.TrimRight(t, " \t"))
		if strings.HasPrefix(up, "COPY ") && strings.HasSuffix(up, "FROM STDIN;") {
			*inCopy = true
			return false
		}
	}
	up := strings.ToUpper(t)
	if engine != "postgres" {
		up = mysqlVersioned.ReplaceAllString(up, "")
		if mysqlOnly.MatchString(up) {
			return true
		}
	}
	if userMapping.MatchString(up) {
		return false
	}
	return forbiddenStatement.MatchString(up) || setPassword.MatchString(up)
}

// cut keeps at most n bytes of s, without a cut-in-half character.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}
