package dbquery

import (
	"fmt"
	"strings"
	"unicode"
)

// statementVerbs are the leading keywords the audit log names. A statement
// that starts with anything else is OTHER, so a literal typed where a keyword
// belongs never reaches the log.
var statementVerbs = map[string]bool{
	"ABORT": true, "ALTER": true, "ANALYSE": true, "ANALYZE": true, "BEGIN": true, "CALL": true,
	"CHECK": true, "CHECKPOINT": true, "CHECKSUM": true, "CLOSE": true, "CLUSTER": true, "COMMENT": true,
	"COMMIT": true, "COPY": true, "CREATE": true, "DEALLOCATE": true, "DECLARE": true, "DELETE": true,
	"DESC": true, "DESCRIBE": true, "DISCARD": true, "DO": true, "DROP": true, "END": true,
	"EXECUTE": true, "EXPLAIN": true, "FETCH": true, "FLUSH": true, "GRANT": true, "HANDLER": true,
	"IMPORT": true, "INSERT": true, "INSTALL": true, "KILL": true, "LISTEN": true, "LOAD": true,
	"LOCK": true, "MERGE": true, "MOVE": true, "NOTIFY": true, "OPTIMIZE": true, "PREPARE": true,
	"PURGE": true, "REASSIGN": true, "REFRESH": true, "REINDEX": true, "RELEASE": true, "RENAME": true,
	"REPAIR": true, "REPLACE": true, "RESET": true, "REVOKE": true, "ROLLBACK": true, "SAVEPOINT": true,
	"SECURITY": true, "SELECT": true, "SET": true, "SHOW": true, "SHUTDOWN": true, "START": true,
	"TABLE": true, "TRUNCATE": true, "UNINSTALL": true, "UNLISTEN": true, "UNLOCK": true, "UPDATE": true,
	"USE": true, "VACUUM": true, "VALUES": true, "WITH": true, "XA": true,
}

// statementObjects are what CREATE, ALTER and DROP name after the verb.
var statementObjects = map[string]bool{
	"AGGREGATE": true, "CAST": true, "COLLATION": true, "CONVERSION": true,
	"DATABASE": true, "DEFAULT": true, "DOMAIN": true, "EVENT": true, "EXTENSION": true,
	"FOREIGN": true, "FUNCTION": true, "GROUP": true, "INDEX": true, "LANGUAGE": true,
	"MATERIALIZED": true, "OPERATOR": true, "POLICY": true, "PROCEDURE": true, "PUBLICATION": true,
	"ROLE": true, "ROUTINE": true, "RULE": true, "SCHEMA": true, "SEQUENCE": true, "SERVER": true,
	"STATISTICS": true, "SUBSCRIPTION": true, "TABLE": true, "TABLESPACE": true, "TRIGGER": true,
	"TYPE": true, "USER": true, "VIEW": true,
}

// statementModifiers sit between CREATE and the object: OR REPLACE, UNIQUE.
var statementModifiers = map[string]bool{
	"OR": true, "REPLACE": true, "UNIQUE": true, "TEMP": true, "TEMPORARY": true, "UNLOGGED": true,
	"GLOBAL": true, "LOCAL": true, "RECURSIVE": true, "TRUSTED": true, "PROCEDURAL": true,
}

// StatementKinds names the kinds of statement in sql, for the audit log:
// "SELECT ×2, ALTER ROLE", in the order they first appear. A kind is the
// leading keyword (and for CREATE, ALTER and DROP the object) when it is on
// a fixed list, else OTHER; nothing else of the text is kept. A batch that
// cannot be split is OTHER.
func StatementKinds(engine, sql string) string {
	my := engine == "mysql" || engine == "mariadb"
	var stmts []Statement
	var err error
	if my {
		stmts, err = SplitMySQL(sql)
	} else {
		stmts, err = SplitPostgres(sql)
	}
	if err != nil {
		return "OTHER"
	}
	var order []string
	count := map[string]int{}
	for _, st := range stmts {
		k := statementKind(skipLeading(st.Text, my))
		if count[k] == 0 {
			order = append(order, k)
		}
		count[k]++
	}
	parts := make([]string, len(order))
	for i, k := range order {
		parts[i] = k
		if count[k] > 1 {
			parts[i] = fmt.Sprintf("%s ×%d", k, count[k])
		}
	}
	return strings.Join(parts, ", ")
}

// statementKind is one statement's kind; s starts at its first keyword.
func statementKind(s string) string {
	verb := leadingWord(s)
	if !statementVerbs[verb] {
		return "OTHER"
	}
	if verb != "CREATE" && verb != "ALTER" && verb != "DROP" {
		return verb
	}
	rest := s[len(verb):]
	for {
		rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
		w := leadingWord(rest)
		switch {
		case statementModifiers[w]:
			rest = rest[len(w):]
		case w == "MATERIALIZED":
			return verb + " MATERIALIZED VIEW"
		case statementObjects[w]:
			return verb + " " + w
		default:
			return verb
		}
	}
}
