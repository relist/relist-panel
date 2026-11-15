package dbquery

import (
	"reflect"
	"testing"
)

func TestRedactSQL(t *testing.T) {
	cases := [][2]string{
		{"ALTER USER u PASSWORD 'x'", "ALTER USER u PASSWORD '***'"},
		{`CREATE USER u IDENTIFIED BY "p;w"`, "CREATE USER u IDENTIFIED BY '***'"},
		{"create user u identified with caching_sha2_password by 'x'", "create user u identified with caching_sha2_password by '***'"},
		{"SELECT 'password'", "SELECT 'password'"},
		{"CREATE ROLE r LOGIN PASSWORD 'it''s'", "CREATE ROLE r LOGIN PASSWORD '***'"},
		{"SET PASSWORD FOR u = PASSWORD('abc')", "SET PASSWORD FOR u = PASSWORD('***')"},
	}
	for _, c := range cases {
		if got := RedactSQL(c[0]); got != c[1] {
			t.Errorf("RedactSQL(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

func TestRedactRedis(t *testing.T) {
	cases := []struct{ in, want []string }{
		{[]string{"AUTH", "u", "p"}, []string{"AUTH", "***", "***"}},
		{[]string{"auth", "p"}, []string{"auth", "***"}},
		{[]string{"CONFIG", "SET", "requirepass", "x"}, []string{"CONFIG", "SET", "requirepass", "***"}},
		{[]string{"config", "set", "maxmemory", "1", "masterauth", "pw"}, []string{"config", "set", "maxmemory", "1", "masterauth", "***"}},
		{[]string{"ACL", "SETUSER", "u", "on", ">pw", "~*", "#abc"}, []string{"ACL", "SETUSER", "u", "on", "***", "~*", "***"}},
		{[]string{"MIGRATE", "h", "6379", "k", "0", "5", "AUTH", "pw"}, []string{"MIGRATE", "h", "6379", "k", "0", "5", "AUTH", "***"}},
		{[]string{"MIGRATE", "h", "6379", "k", "0", "5", "AUTH2", "u", "pw", "KEYS", "a"}, []string{"MIGRATE", "h", "6379", "k", "0", "5", "AUTH2", "***", "***", "KEYS", "a"}},
		{[]string{"GET", "k"}, []string{"GET", "k"}},
	}
	for _, c := range cases {
		if got := RedactRedis(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("RedactRedis(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
