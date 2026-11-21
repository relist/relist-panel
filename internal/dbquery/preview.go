package dbquery

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// previewBase is the first sentence of every Preview refusal.
func previewBase(engine string) string {
	if engine == "postgres" {
		return "Preview runs only INSERT, UPDATE and DELETE statements."
	}
	return "Preview runs only INSERT, UPDATE, DELETE and REPLACE statements."
}

// skipLeading drops whitespace and plain comments from the front of a
// statement. An executable comment (/*! or /*M!) stays: it is the caller's
// to refuse. mysqlComments also drops # lines.
func skipLeading(s string, mysqlComments bool) string {
	for {
		s = strings.TrimLeftFunc(s, unicode.IsSpace)
		switch {
		case strings.HasPrefix(s, "--") || (mysqlComments && strings.HasPrefix(s, "#")):
			n := strings.IndexByte(s, '\n')
			if n < 0 {
				return ""
			}
			s = s[n+1:]
		case strings.HasPrefix(s, "/*") && !strings.HasPrefix(s, "/*!") && !strings.HasPrefix(s, "/*M!"):
			depth, i := 0, 0
			for i < len(s) {
				if strings.HasPrefix(s[i:], "/*") {
					depth++
					i += 2
					continue
				}
				if strings.HasPrefix(s[i:], "*/") {
					depth--
					i += 2
					if depth == 0 {
						break
					}
					continue
				}
				i++
			}
			if depth != 0 {
				return s // unterminated; the keyword check refuses it
			}
			s = s[i:]
		default:
			return s
		}
	}
}

// leadingWord is the upper-cased keyword a statement starts with.
func leadingWord(s string) string {
	i := 0
	for i < len(s) && (s[i] == '_' || ('a' <= s[i] && s[i] <= 'z') || ('A' <= s[i] && s[i] <= 'Z')) {
		i++
	}
	if i == 0 {
		if f := strings.Fields(s); len(f) > 0 {
			w := f[0]
			if len(w) > 20 {
				w = w[:20]
			}
			return w
		}
		return ""
	}
	return strings.ToUpper(s[:i])
}

// PreviewStatements splits sql for a Preview and refuses anything that is
// not plain DML: every statement must start with INSERT, UPDATE or DELETE
// (REPLACE too on MySQL / MariaDB), executable comments included. DDL is out
// because MySQL commits it implicitly, and COMMIT would make the dry run
// real.
func PreviewStatements(engine, sql string) ([]Statement, *QueryError) {
	var stmts []Statement
	var err error
	if engine == "postgres" {
		stmts, err = SplitPostgres(sql)
	} else {
		stmts, err = SplitMySQL(sql)
	}
	if err != nil {
		return nil, &QueryError{Message: err.Error()}
	}
	if len(stmts) == 0 {
		return nil, &QueryError{Message: "there is nothing to run"}
	}
	my := engine != "postgres"
	for i, st := range stmts {
		lead := skipLeading(st.Text, my)
		if strings.HasPrefix(lead, "/*!") || strings.HasPrefix(lead, "/*M!") {
			return nil, &QueryError{
				Message:        fmt.Sprintf("%s Statement %d starts with an executable comment.", previewBase(engine), i+1),
				StatementIndex: i, StatementStart: st.Start,
			}
		}
		if my && (strings.Contains(st.Text, "/*!") || strings.Contains(st.Text, "/*M!")) {
			// Anywhere in the statement, a string literal included: refusing a
			// legitimate /*! inside data is safe, running one is not.
			return nil, &QueryError{
				Message:        fmt.Sprintf("%s Statement %d holds an executable comment.", previewBase(engine), i+1),
				StatementIndex: i, StatementStart: st.Start,
			}
		}
		word := leadingWord(lead)
		if word == "INSERT" || word == "UPDATE" || word == "DELETE" || (my && word == "REPLACE") {
			continue
		}
		return nil, &QueryError{
			Message:        fmt.Sprintf("%s Statement %d starts with %s.", previewBase(engine), i+1, word),
			StatementIndex: i, StatementStart: st.Start,
		}
	}
	return stmts, nil
}

// previewSkipwords sit between the verb and the table.
var previewSkipwords = map[string]bool{
	"LOW_PRIORITY": true, "QUICK": true, "IGNORE": true, "DELAYED": true,
	"HIGH_PRIORITY": true, "INTO": true, "FROM": true, "ONLY": true,
}

// PreviewTarget names what a DML statement touches: its verb, lower case,
// and the first table, quotes removed and a schema prefix kept. table is ""
// when the statement names several (DELETE t1, t2 FROM …) or none it can
// read.
func PreviewTarget(stmt string) (verb, table string) {
	s := skipLeading(stmt, true)
	word := leadingWord(s)
	verb = strings.ToLower(word)
	s = strings.TrimLeftFunc(s[len(word):], unicode.IsSpace)
	for {
		w := leadingWord(s)
		if w == "" || !previewSkipwords[w] {
			break
		}
		s = strings.TrimLeftFunc(s[len(w):], unicode.IsSpace)
	}
	name, rest, ok := previewIdent(s)
	if !ok {
		return verb, ""
	}
	if strings.HasPrefix(strings.TrimLeftFunc(rest, unicode.IsSpace), ",") {
		return verb, ""
	}
	return verb, name
}

// previewIdent reads a possibly quoted, possibly schema-qualified name.
func previewIdent(s string) (name, rest string, ok bool) {
	var parts []string
	for {
		part, after, found := previewIdentPart(s)
		if !found {
			break
		}
		parts = append(parts, part)
		s = after
		if strings.HasPrefix(s, ".") {
			s = s[1:]
			continue
		}
		break
	}
	if len(parts) == 0 {
		return "", "", false
	}
	return strings.Join(parts, "."), s, true
}

func previewIdentPart(s string) (part, rest string, ok bool) {
	if s == "" {
		return "", "", false
	}
	if q := s[0]; q == '"' || q == '`' {
		var b strings.Builder
		i := 1
		for i < len(s) {
			if s[i] == q {
				if i+1 < len(s) && s[i+1] == q {
					b.WriteByte(q)
					i += 2
					continue
				}
				return b.String(), s[i+1:], true
			}
			b.WriteByte(s[i])
			i++
		}
		return "", "", false
	}
	i := 0
	for i < len(s) {
		r := rune(s[i])
		if r == '_' || r == '$' || ('0' <= s[i] && s[i] <= '9') || ('a' <= s[i] && s[i] <= 'z') || ('A' <= s[i] && s[i] <= 'Z') {
			i++
			continue
		}
		break
	}
	if i == 0 {
		return "", "", false
	}
	return s[:i], s[i:], true
}

// mysqlPreviewRefusal refuses a MySQL / MariaDB Preview while any non-system
// schema holds a table whose engine would not roll back, naming up to five.
func mysqlPreviewRefusal(ctx context.Context, q queryer) (*QueryResponse, error) {
	const stmt = `SELECT TABLE_SCHEMA, TABLE_NAME, ENGINE FROM information_schema.TABLES WHERE ENGINE IS NOT NULL AND ENGINE <> 'InnoDB' AND TABLE_TYPE NOT IN ('VIEW','SEQUENCE') AND TABLE_SCHEMA NOT IN ('information_schema','performance_schema','mysql','sys') ORDER BY 1, 2 LIMIT 5`
	cur, err := q.query(ctx, stmt)
	if err != nil {
		return nil, err
	}
	defer cur.Close()
	if _, err := cur.Columns(); err != nil {
		return nil, err
	}
	var tables []string
	for cur.Next() {
		cells, err := cur.Cells()
		if err != nil {
			return nil, err
		}
		if len(cells) >= 3 {
			tables = append(tables, fmt.Sprintf("%v.%v (%v)", cells[0], cells[1], cells[2]))
		}
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return nil, nil
	}
	msg := "Preview needs every table to be InnoDB, so that its changes roll back. Not InnoDB: " + strings.Join(tables, ", ")
	return &QueryResponse{Results: []QueryResult{}, Preview: true, Error: &QueryError{Message: msg}}, nil
}

// runPreviewBatch wraps runBatch between START TRANSACTION and a ROLLBACK
// that always runs, and stamps each result with its statement's verb and
// table.
func runPreviewBatch(ctx context.Context, q queryer, stmts []Statement, stop func()) (resp QueryResponse, num uint16) {
	cur, err := q.query(ctx, "START TRANSACTION")
	if err != nil {
		return QueryResponse{Results: []QueryResult{}, Preview: true, Error: &QueryError{Message: err.Error()}}, 0
	}
	_ = cur.Close()
	defer func() {
		rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if cur, err := q.query(rctx, "ROLLBACK"); err == nil {
			_ = cur.Close()
		}
		resp.Preview = true
		for i := range resp.Results {
			if idx := resp.Results[i].StatementIndex; idx >= 0 && idx < len(stmts) {
				resp.Results[i].Verb, resp.Results[i].Target = PreviewTarget(stmts[idx].Text)
			}
		}
	}()
	resp, num = runBatch(ctx, q, stmts, stop)
	return resp, num
}
