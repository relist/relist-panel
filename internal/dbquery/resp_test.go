package dbquery

import (
	"fmt"
	"net"
	"strings"
	"testing"
)

// serve answers each request the connection receives with the next script
// entry, then stops.
func serve(t *testing.T, script ...string) *respConn {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })
	go func() {
		buf := make([]byte, 4096)
		for _, reply := range script {
			if _, err := server.Read(buf); err != nil {
				return
			}
			if _, err := server.Write([]byte(reply)); err != nil {
				return
			}
		}
	}()
	return newRespConn(client)
}

func bulk(s string) string { return fmt.Sprintf("$%d\r\n%s\r\n", len(s), s) }

func TestRespReadCaps(t *testing.T) {
	big := strings.Repeat("x", 100000)
	elems := new(strings.Builder)
	fmt.Fprintf(elems, "*%d\r\n", 1500)
	for i := 0; i < 1500; i++ {
		elems.WriteString(bulk(fmt.Sprintf("e%d", i)))
	}
	c := serve(t,
		bulk(big),
		elems.String(),
		"-ERR unknown command 'FOO'\r\n",
		"*2\r\n*2\r\n:1\r\n$-1\r\n+OK\r\n",
		bulk("after"),
	)

	r, err := c.Do("GET", "big")
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := r.Value.(string); r.Type != "bulk" || len(v) != 65536 || r.Bytes != 100000 {
		t.Fatalf("big bulk = type %s len %d bytes %d", r.Type, len(fmt.Sprint(r.Value)), r.Bytes)
	}

	r, err = c.Do("LRANGE", "l", "0", "-1")
	if err != nil {
		t.Fatal(err)
	}
	list, _ := r.Value.([]Reply)
	if r.Type != "array" || len(list) != 1000 || r.More != 500 || list[999].Value != "e999" {
		t.Fatalf("array = type %s kept %d more %d", r.Type, len(list), r.More)
	}

	r, err = c.Do("FOO")
	if err != nil || r.Type != "error" || r.Value != "ERR unknown command 'FOO'" {
		t.Fatalf("error reply = %+v err %v", r, err)
	}

	r, err = c.Do("X")
	if err != nil {
		t.Fatal(err)
	}
	nested, _ := r.Value.([]Reply)
	inner, _ := nested[0].Value.([]Reply)
	if len(nested) != 2 || len(inner) != 2 || inner[0].Type != "integer" || inner[0].Value != int64(1) || inner[1].Type != "nil" || nested[1].Type != "string" || nested[1].Value != "OK" {
		t.Fatalf("nested = %+v", r)
	}

	// The reply after the capped ones still parses: nothing was left on the wire.
	r, err = c.Do("GET", "k")
	if err != nil || r.Value != "after" || r.Bytes != 0 {
		t.Fatalf("next reply = %+v err %v", r, err)
	}
}

func TestRespSkipsTheRestOfAHugeArray(t *testing.T) {
	// 3000 elements, each an array of two: the 2000 over the cap are read off
	// the wire, nested or not, so the next reply stays aligned.
	b := new(strings.Builder)
	b.WriteString("*3000\r\n")
	for i := 0; i < 3000; i++ {
		b.WriteString("*2\r\n" + bulk("a") + ":7\r\n")
	}
	c := serve(t, b.String(), ":1\r\n")
	r, err := c.Do("X")
	if err != nil {
		t.Fatal(err)
	}
	if list, _ := r.Value.([]Reply); len(list) != 1000 || r.More != 2000 {
		t.Fatalf("kept %d more %d", len(list), r.More)
	}
	if r, err = c.Do("Y"); err != nil || r.Value != int64(1) {
		t.Fatalf("next = %+v err %v", r, err)
	}
}
