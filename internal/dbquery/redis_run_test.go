package dbquery

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeRedis is a RESP2 server on loopback. It answers AUTH, SELECT and
// COMMAND INFO itself and records every other command; handle answers those.
type fakeRedis struct {
	mu     sync.Mutex
	sent   [][]string
	flags  map[string]string // command -> its flags
	handle func(args []string) string
	Target Target
}

func newFakeRedis(t *testing.T, handle func(args []string) string) *fakeRedis {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRedis{handle: handle, flags: map[string]string{
		"get": "readonly fast", "set": "write denyoom", "flushall": "write", "scan": "readonly", "type": "readonly fast", "pttl": "readonly fast",
		"hscan": "readonly", "hlen": "readonly fast", "lrange": "readonly", "llen": "readonly fast",
	}}
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	n, _ := strconv.Atoi(port)
	f.Target = Target{Host: host, Port: n, Password: "secret"}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func readCommand(br *bufio.Reader) ([]string, error) {
	line, err := br.ReadString('\n')
	if err != nil {
		return nil, err
	}
	n, _ := strconv.Atoi(strings.TrimSpace(line[1:]))
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		hdr, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		size, _ := strconv.Atoi(strings.TrimSpace(hdr[1:]))
		buf := make([]byte, size+2)
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:size]))
	}
	return args, nil
}

func (f *fakeRedis) serve(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	for {
		args, err := readCommand(br)
		if err != nil {
			return
		}
		switch strings.ToUpper(args[0]) {
		case "AUTH", "SELECT":
			fmt.Fprint(c, "+OK\r\n")
			continue
		case "COMMAND":
			name := strings.ToLower(args[2])
			fl, ok := f.flags[name]
			if !ok {
				fmt.Fprint(c, "*1\r\n$-1\r\n")
				continue
			}
			parts := strings.Fields(fl)
			fmt.Fprintf(c, "*1\r\n*10\r\n$%d\r\n%s\r\n:2\r\n*%d\r\n", len(name), name, len(parts))
			for _, p := range parts {
				fmt.Fprintf(c, "+%s\r\n", p)
			}
			fmt.Fprint(c, ":1\r\n:1\r\n:1\r\n*0\r\n*0\r\n*0\r\n*0\r\n")
			continue
		}
		f.mu.Lock()
		f.sent = append(f.sent, args)
		f.mu.Unlock()
		fmt.Fprint(c, f.handle(args))
	}
}

func (f *fakeRedis) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []string{}
	for _, a := range f.sent {
		out = append(out, strings.Join(a, " "))
	}
	return out
}

func TestRunRedisNeedsConfirm(t *testing.T) {
	f := newFakeRedis(t, func([]string) string { return "+OK\r\n" })
	got := RunRedis(context.Background(), f.Target, 0, "FLUSHALL", true, false)
	if !got.NeedsConfirm || got.Reply != nil || got.Refused != "" || len(f.commands()) != 0 {
		t.Fatalf("resp = %+v, sent %v: a dangerous command must wait for the second confirmation", got, f.commands())
	}
	got = RunRedis(context.Background(), f.Target, 0, "FLUSHALL", true, true)
	if got.NeedsConfirm || got.Reply == nil || !reflect.DeepEqual(f.commands(), []string{"FLUSHALL"}) {
		t.Fatalf("confirmed: resp = %+v, sent %v", got, f.commands())
	}
}

func TestRunRedisRefuses(t *testing.T) {
	f := newFakeRedis(t, func([]string) string { return "+OK\r\n" })
	for line, reason := range map[string]string{
		"SET k v":     "writes are off",
		"FOO":         "unknown command",
		"SUBSCRIBE c": "not available in the console",
		`SET k "oops`: "unbalanced quotes",
		"":            "nothing to run",
		"AUTH x":      "not available in the console",
	} {
		got := RunRedis(context.Background(), f.Target, 0, line, false, false)
		if !strings.Contains(got.Refused+got.Error, reason) || got.Reply != nil {
			t.Errorf("%q = %+v, want %q", line, got, reason)
		}
	}
	if n := len(f.commands()); n != 0 {
		t.Fatalf("refused commands reached the server: %v", f.commands())
	}
	got := RunRedis(context.Background(), f.Target, 0, "GET k", false, false)
	if got.Reply == nil || got.Refused != "" {
		t.Fatalf("GET = %+v", got)
	}
}

func TestRunRedisHidesAuthFailures(t *testing.T) {
	got := RunRedis(context.Background(), Target{Host: "127.0.0.1", Port: 1, Password: "secret"}, 0, "GET k", false, false)
	if got.Error == "" || strings.Contains(got.Error, "secret") {
		t.Fatalf("error = %q", got.Error)
	}
}

func TestScanKeys(t *testing.T) {
	f := newFakeRedis(t, func(a []string) string {
		switch a[0] {
		case "SCAN":
			return "*2\r\n$2\r\n17\r\n*2\r\n$1\r\na\r\n$1\r\nb\r\n"
		case "TYPE":
			return "+" + map[string]string{"a": "string", "b": "hash"}[a[1]] + "\r\n"
		case "PTTL":
			return map[string]string{"a": ":-1", "b": ":5000"}[a[1]] + "\r\n"
		}
		return "-ERR\r\n"
	})
	keys, next, err := ScanKeys(context.Background(), f.Target, 0, "", "0")
	if err != nil || next != "17" {
		t.Fatalf("next %q err %v", next, err)
	}
	want := []KeyInfo{{Key: "a", Type: "string", TTLms: -1}, {Key: "b", Type: "hash", TTLms: 5000}}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys = %+v", keys)
	}
	if got := f.commands()[0]; got != "SCAN 0 MATCH * COUNT 100" {
		t.Fatalf("first command %q", got)
	}
}

func TestReadKeyHashMoreThanShown(t *testing.T) {
	f := newFakeRedis(t, func(a []string) string {
		switch a[0] {
		case "TYPE":
			return "+hash\r\n"
		case "PTTL":
			return ":-1\r\n"
		case "HSCAN":
			return "*2\r\n$2\r\n99\r\n*4\r\n$1\r\nf\r\n$1\r\nv\r\n$2\r\nf2\r\n$2\r\nv2\r\n"
		case "HLEN":
			return ":1500\r\n"
		}
		return "-ERR\r\n"
	})
	kv, err := ReadKey(context.Background(), f.Target, 0, "h")
	if err != nil {
		t.Fatal(err)
	}
	if kv.Type != "hash" || kv.Total != 1500 || !kv.More || len(kv.Items) != 2 || kv.Items[1].Field != "f2" || kv.Items[1].Value != "v2" {
		t.Fatalf("kv = %+v", kv)
	}
}

func TestReadKeyUnsupportedType(t *testing.T) {
	f := newFakeRedis(t, func(a []string) string {
		if a[0] == "TYPE" {
			return "+stream\r\n"
		}
		return ":-1\r\n"
	})
	kv, err := ReadKey(context.Background(), f.Target, 0, "s")
	if err != nil || kv.Type != "stream" || !kv.Unsupported || len(kv.Items) != 0 {
		t.Fatalf("kv = %+v err %v", kv, err)
	}
}

func TestReadKeyCutsASetAtAThousand(t *testing.T) {
	// SSCAN's COUNT is only a hint: a page can hold more than 1,000 members.
	page := new(strings.Builder)
	page.WriteString("*2\r\n$1\r\n0\r\n*1200\r\n")
	for i := 0; i < 1200; i++ {
		fmt.Fprintf(page, "$%d\r\nm%d\r\n", len(fmt.Sprint(i))+1, i)
	}
	f := newFakeRedis(t, func(a []string) string {
		switch a[0] {
		case "TYPE":
			return "+set\r\n"
		case "SSCAN":
			return page.String()
		case "SCARD":
			return ":1200\r\n"
		}
		return ":-1\r\n"
	})
	kv, err := ReadKey(context.Background(), f.Target, 0, "s")
	if err != nil {
		t.Fatal(err)
	}
	if len(kv.Items) != 1000 || !kv.More || kv.Total != 1200 {
		t.Fatalf("items %d more %v total %d", len(kv.Items), kv.More, kv.Total)
	}
}

func TestRunRedisGranteeNeverConfirms(t *testing.T) {
	f := newFakeRedis(t, func([]string) string { return "+OK\r\n" })
	got := RunRedisGrantee(context.Background(), f.Target, 0, "FLUSHALL", true)
	if got.Refused == "" || got.NeedsConfirm || got.Reply != nil || len(f.commands()) != 0 {
		t.Fatalf("resp = %+v, sent %v: a grantee's FLUSHALL is refused outright", got, f.commands())
	}
}
