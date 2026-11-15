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
		{"EVAL read-only", eval, []string{"EVAL", "return 1", "0"}, false, true, false, "writes are off"},
		{"EVAL with writes", eval, []string{"EVAL", "return 1", "0"}, true, false, false, ""},
		{"EVAL_RO read-only", evalRO, []string{"EVAL_RO", "return 1", "0"}, false, false, false, ""},
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
		{"SCRIPT FLUSH writes", script, []string{"SCRIPT", "FLUSH"}, true, false, true, ""},
		{"SCRIPT EXISTS read-only", script, []string{"SCRIPT", "EXISTS", "x"}, false, true, false, "writes are off"},
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
