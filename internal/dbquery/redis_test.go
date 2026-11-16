package dbquery

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitRedisArgs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		err  bool
	}{
		{`set k "a b"`, []string{"set", "k", "a b"}, false},
		{`  get   k  `, []string{"get", "k"}, false},
		{`"\x41\n"`, []string{"A\n"}, false},
		{`"\t\r\b\a\\\""`, []string{"\t\r\b\a\\\""}, false},
		{`'it\'s'`, []string{"it's"}, false},
		{`'a\nb'`, []string{`a\nb`}, false},
		{`"a"b`, nil, true},
		{`'a'b`, nil, true},
		{`"open`, nil, true},
		{`'open`, nil, true},
		{``, []string{}, false},
	}
	for _, c := range cases {
		got, err := SplitRedisArgs(c.in)
		if c.err {
			if err == nil {
				t.Errorf("%q: want an error, got %q", c.in, got)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

// The flags below are what COMMAND INFO answered on Redis 8 and 7.4.
func info(flags string, subs map[string]string) CommandInfo {
	i := CommandInfo{Known: true, Flags: strings.Fields(flags)}
	if subs != nil {
		i.Subcommands = map[string][]string{}
		for name, f := range subs {
			i.Subcommands[name] = strings.Fields(f)
		}
	}
	return i
}

func TestClassify(t *testing.T) {
	get := info("readonly fast", nil)
	set := info("write denyoom", nil)
	eval := info("noscript stale skip_monitor no_mandatory_keys movablekeys", nil)
	evalRO := info("readonly noscript stale skip_monitor no_mandatory_keys movablekeys", nil)
	config := info("", map[string]string{"set": "admin noscript loading stale", "get": "admin noscript loading stale", "resetstat": "admin noscript loading stale"})
	script := info("", map[string]string{"flush": "noscript", "load": "noscript stale", "exists": "noscript"})
	flushall := info("write", nil)
	subscribe := info("denyoom pubsub noscript loading stale", nil)
	ping := info("fast", nil)
	slowlog := info("", map[string]string{"get": "admin loading stale", "reset": "admin loading stale"})

	cases := []struct {
		name      string
		info      CommandInfo
		args      []string
		writes    bool
		refused   bool
		dangerous bool
		reason    string
	}{
		{"GET read-only", get, []string{"GET", "k"}, false, false, false, ""},
		{"SET read-only", set, []string{"SET", "k", "v"}, false, true, false, "writes are off"},
		{"EVAL read-only", eval, []string{"EVAL", "return 1", "0"}, false, true, false, "EVAL is not available in the console"},
		{"EVAL with writes", eval, []string{"EVAL", "return 1", "0"}, true, true, false, "EVAL is not available in the console"},
		{"EVAL_RO read-only", evalRO, []string{"EVAL_RO", "return 1", "0"}, false, true, false, "EVAL_RO is not available in the console"},
		{"CONFIG GET read-only", config, []string{"CONFIG", "GET", "maxmemory"}, false, false, false, ""},
		{"CONFIG SET read-only", config, []string{"CONFIG", "SET", "maxmemory", "1"}, false, true, true, "writes are off"},
		{"CONFIG SET writes", config, []string{"config", "set", "maxmemory", "1"}, true, false, true, ""},
		{"CONFIG alone", config, []string{"CONFIG"}, true, true, false, "needs a subcommand"},
		{"CONFIG FOO", config, []string{"CONFIG", "FOO"}, true, true, false, "unknown subcommand"},
		{"FLUSHALL writes", flushall, []string{"FLUSHALL"}, true, false, true, ""},
		{"FLUSHALL read-only", flushall, []string{"FLUSHALL"}, false, true, true, "writes are off"},
		{"SUBSCRIBE writes", subscribe, []string{"SUBSCRIBE", "c"}, true, true, false, "SUBSCRIBE is not available in the console"},
		{"SELECT", info("loading stale fast", nil), []string{"SELECT", "1"}, true, true, false, "use the database selector"},
		{"FOO", CommandInfo{}, []string{"FOO"}, true, true, false, "unknown command"},
		{"SCRIPT FLUSH writes", script, []string{"SCRIPT", "FLUSH"}, true, true, false, "SCRIPT is not available in the console"},
		{"SCRIPT EXISTS read-only", script, []string{"SCRIPT", "EXISTS", "x"}, false, true, false, "SCRIPT is not available in the console"},
		{"PING read-only", ping, []string{"PING"}, false, false, false, ""},
		{"SLOWLOG GET read-only", slowlog, []string{"SLOWLOG", "GET"}, false, false, false, ""},
		{"SLOWLOG RESET read-only", slowlog, []string{"SLOWLOG", "RESET"}, false, true, false, "writes are off"},
		{"CLIENT REPLY", info("", map[string]string{"reply": "noscript loading stale"}), []string{"CLIENT", "REPLY", "OFF"}, true, true, false, "CLIENT REPLY is not available"},
		{"MULTI", info("noscript loading stale fast allow_busy", nil), []string{"multi"}, true, true, false, "MULTI is not available"},
	}
	for _, c := range cases {
		got := Classify(c.info, c.args, c.writes)
		if got.Refused != c.refused || got.Dangerous != c.dangerous || !strings.Contains(got.Reason, c.reason) {
			t.Errorf("%s: %+v, want refused=%v dangerous=%v reason~%q", c.name, got, c.refused, c.dangerous, c.reason)
		}
	}
}

func TestParseCommandInfo(t *testing.T) {
	str := func(s string) Reply { return Reply{Type: "string", Value: s} }
	flags := func(f ...string) Reply {
		out := make([]Reply, len(f))
		for i, s := range f {
			out[i] = str(s)
		}
		return Reply{Type: "array", Value: out}
	}
	entry := func(name string, f Reply, subs ...Reply) Reply {
		row := []Reply{{Type: "bulk", Value: name}, {Type: "integer", Value: int64(-2)}, f,
			{Type: "integer", Value: int64(0)}, {Type: "integer", Value: int64(0)}, {Type: "integer", Value: int64(0)},
			flags(), flags(), flags(), {Type: "array", Value: subs}}
		return Reply{Type: "array", Value: row}
	}
	sub := entry("config|get", flags("admin", "noscript"))
	got := ParseCommandInfo(Reply{Type: "array", Value: []Reply{entry("config", flags(), sub)}})
	if !got.Known || len(got.Flags) != 0 || !reflect.DeepEqual(got.Subcommands["get"], []string{"admin", "noscript"}) {
		t.Fatalf("config = %+v", got)
	}
	got = ParseCommandInfo(Reply{Type: "array", Value: []Reply{entry("get", flags("readonly", "fast"))}})
	if !got.Known || !reflect.DeepEqual(got.Flags, []string{"readonly", "fast"}) || got.Subcommands != nil {
		t.Fatalf("get = %+v", got)
	}
	if got = ParseCommandInfo(Reply{Type: "array", Value: []Reply{{Type: "nil"}}}); got.Known {
		t.Fatalf("unknown command is Known: %+v", got)
	}
	if got = ParseCommandInfo(Reply{Type: "error", Value: "ERR"}); got.Known {
		t.Fatalf("error reply is Known: %+v", got)
	}
}

// infoCat is info() plus ACL categories.
func infoCat(flags, cats string, subFlags, subCats map[string]string) CommandInfo {
	i := info(flags, subFlags)
	i.Categories = strings.Fields(cats)
	if subCats != nil {
		i.SubcommandCategories = map[string][]string{}
		for name, c := range subCats {
			i.SubcommandCategories[name] = strings.Fields(c)
		}
	}
	return i
}

func TestParseCommandInfoCategories(t *testing.T) {
	str := func(s string) Reply { return Reply{Type: "string", Value: s} }
	arr := func(f ...string) Reply {
		out := make([]Reply, len(f))
		for i, s := range f {
			out[i] = str(s)
		}
		return Reply{Type: "array", Value: out}
	}
	entry := func(name string, flags, cats Reply, subs ...Reply) Reply {
		row := []Reply{{Type: "bulk", Value: name}, {Type: "integer", Value: int64(-2)}, flags,
			{Type: "integer", Value: int64(0)}, {Type: "integer", Value: int64(0)}, {Type: "integer", Value: int64(0)},
			cats, arr(), arr(), {Type: "array", Value: subs}}
		return Reply{Type: "array", Value: row}
	}
	sub := entry("config|get", arr("admin", "noscript"), arr("@admin", "@slow"))
	got := ParseCommandInfo(Reply{Type: "array", Value: []Reply{entry("config", arr(), arr("@admin", "@slow", "@dangerous"), sub)}})
	if !reflect.DeepEqual(got.Categories, []string{"@admin", "@slow", "@dangerous"}) {
		t.Fatalf("Categories = %+v", got)
	}
	if !reflect.DeepEqual(got.SubcommandCategories["get"], []string{"@admin", "@slow"}) {
		t.Fatalf("SubcommandCategories = %+v", got)
	}
}

func TestClassifyGrantee(t *testing.T) {
	// CommandInfo written by hand from Redis 7.4's COMMAND INFO.
	get := infoCat("readonly fast", "@read @string @fast", nil, nil)
	scan := infoCat("readonly", "@keyspace @read @slow", nil, nil)
	ttl := infoCat("readonly fast", "@keyspace @read @fast", nil, nil)
	ping := infoCat("fast", "@fast @connection", nil, nil)
	set := infoCat("write denyoom", "@write @string @slow", nil, nil)
	del := infoCat("write", "@keyspace @write @slow", nil, nil)
	keys := infoCat("readonly", "@keyspace @read @slow @dangerous", nil, nil)
	infoCmd := infoCat("loading stale", "@slow @dangerous", nil, nil)
	migrate := infoCat("write movablekeys", "@keyspace @write @slow @dangerous", nil, nil)
	flushdb := infoCat("write", "@keyspace @write @slow @dangerous", nil, nil)
	config := infoCat("", "@admin @slow @dangerous",
		map[string]string{"get": "admin noscript loading stale", "set": "admin noscript loading stale"},
		map[string]string{"get": "@admin @slow @dangerous", "set": "@admin @slow @dangerous"})
	client := infoCat("", "@slow",
		map[string]string{"list": "admin noscript loading stale", "kill": "admin noscript loading stale"},
		map[string]string{"list": "@admin @slow @dangerous", "kill": "@admin @slow @dangerous"})
	slowlog := infoCat("", "@slow",
		map[string]string{"get": "admin loading stale"}, map[string]string{"get": "@admin @slow @dangerous"})
	acl := infoCat("", "@slow",
		map[string]string{"list": "admin noscript loading stale"}, map[string]string{"list": "@admin @slow @dangerous"})
	evalRO := infoCat("readonly noscript stale skip_monitor no_mandatory_keys movablekeys", "@slow @scripting", nil, nil)
	eval := infoCat("noscript stale skip_monitor no_mandatory_keys movablekeys", "@slow @scripting", nil, nil)
	function := infoCat("", "@slow", map[string]string{"load": "write denyoom noscript"}, map[string]string{"load": "@write @slow @scripting"})

	cases := []struct {
		name      string
		info      CommandInfo
		args      []string
		readWrite bool
		refused   bool
		reason    string
	}{
		{"GET read", get, []string{"GET", "k"}, false, false, ""},
		{"SCAN read", scan, []string{"SCAN", "0"}, false, false, ""},
		{"TTL read", ttl, []string{"TTL", "k"}, false, false, ""},
		{"PING read", ping, []string{"PING"}, false, false, ""},
		{"SET read", set, []string{"SET", "k", "v"}, false, true, "writes are off"},
		{"CONFIG GET read", config, []string{"CONFIG", "GET", "requirepass"}, false, true, "CONFIG GET is not available to non-Admins"},
		{"INFO read", infoCmd, []string{"INFO"}, false, true, "INFO is not available to non-Admins"},
		{"KEYS read", keys, []string{"KEYS", "*"}, false, true, "KEYS is not available to non-Admins"},
		{"CLIENT LIST read", client, []string{"CLIENT", "LIST"}, false, true, "CLIENT LIST is not available to non-Admins"},
		{"SLOWLOG GET read", slowlog, []string{"SLOWLOG", "GET"}, false, true, "SLOWLOG GET is not available to non-Admins"},
		{"EVAL_RO read", evalRO, []string{"EVAL_RO", "return 1", "0"}, false, true, "not available in the console"},
		{"SET read-write", set, []string{"SET", "k", "v"}, true, false, ""},
		{"DEL read-write", del, []string{"DEL", "k"}, true, false, ""},
		{"FLUSHDB read-write", flushdb, []string{"FLUSHDB"}, true, true, "FLUSHDB is not available to non-Admins"},
		{"CONFIG SET read-write", config, []string{"CONFIG", "SET", "maxmemory", "1"}, true, true, "CONFIG SET is not available to non-Admins"},
		{"CLIENT KILL read-write", client, []string{"CLIENT", "KILL", "ID", "1"}, true, true, "CLIENT KILL is not available to non-Admins"},
		{"MIGRATE read-write", migrate, []string{"MIGRATE", "h", "6379", "k", "0", "5000"}, true, true, "MIGRATE is not available to non-Admins"},
		{"ACL LIST read-write", acl, []string{"ACL", "LIST"}, true, true, "ACL LIST is not available to non-Admins"},
		{"EVAL read-write", eval, []string{"EVAL", "return 1", "0"}, true, true, "not available in the console"},
		{"FUNCTION LOAD read-write", function, []string{"FUNCTION", "LOAD", "x"}, true, true, "not available in the console"},
	}
	for _, c := range cases {
		got := ClassifyGrantee(c.info, c.args, c.readWrite)
		if got.Refused != c.refused || !strings.Contains(got.Reason, c.reason) {
			t.Errorf("%s: %+v, want refused=%v reason~%q", c.name, got, c.refused, c.reason)
		}
		if got.Dangerous {
			t.Errorf("%s: grantee result must never be Dangerous: %+v", c.name, got)
		}
	}
}

func TestClassifyAdminRefusesScripts(t *testing.T) {
	eval := info("noscript stale skip_monitor no_mandatory_keys movablekeys", nil)
	fcall := info("noscript stale skip_monitor no_mandatory_keys movablekeys", nil)
	for _, args := range [][]string{{"EVAL", "return 1", "0"}, {"FCALL", "f", "0"}} {
		got := Classify(eval, args, true)
		if args[0] == "FCALL" {
			got = Classify(fcall, args, true)
		}
		if !got.Refused || !strings.Contains(got.Reason, "not available in the console") {
			t.Errorf("%s: %+v, want refused in the console", args[0], got)
		}
	}
}
