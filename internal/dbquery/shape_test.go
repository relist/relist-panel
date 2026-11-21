package dbquery

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestShapeCell(t *testing.T) {
	if got := ShapeCell([]byte("9007199254740993"), false); got != "9007199254740993" {
		t.Errorf("bigint = %#v", got)
	}
	if got := ShapeCell(nil, true); got != nil {
		t.Errorf("null = %#v", got)
	}
	if got := ShapeCell([]byte{}, false); got != "" {
		t.Errorf("empty = %#v", got)
	}
	if got := ShapeCell([]byte{0xff, 0xfe}, false); got != (CutCell{Hex: "fffe", Bytes: 2}) {
		t.Errorf("binary = %#v", got)
	}
	long := strings.Repeat("é", 35000) // 70,000 bytes, two per rune
	c, ok := ShapeCell([]byte(long), false).(CutCell)
	if !ok || c.Bytes != 70000 || len(c.Text) > 65536 || len(c.Text) < 65534 || !utf8.ValidString(c.Text) || c.Hex != "" {
		t.Errorf("long text: ok=%v bytes=%d len=%d", ok, c.Bytes, len(c.Text))
	}
	bin := make([]byte, 70000)
	bin[0] = 0xff
	b := ShapeCell(bin, false).(CutCell)
	if b.Bytes != 70000 || len(b.Hex) != 65536 {
		t.Errorf("long binary: bytes=%d hexlen=%d", b.Bytes, len(b.Hex))
	}
	if got := ShapeCell([]byte(strings.Repeat("a", 65536)), false); got == nil {
		t.Error("exact cap")
	} else if _, isStr := got.(string); !isStr {
		t.Errorf("a cell of exactly maxCell stays a string, got %#v", got)
	}
}

func TestBudget(t *testing.T) {
	var b Budget
	if !b.Result() {
		t.Fatal("first result")
	}
	for i := 0; i < 1000; i++ {
		if !b.Row([]Cell{"x"}) {
			t.Fatalf("row %d refused", i+1)
		}
	}
	if b.Row([]Cell{"x"}) {
		t.Fatal("row 1001 kept")
	}
	if b.Stopped() != "Stopped after 1,000 rows." {
		t.Errorf("stopped = %q", b.Stopped())
	}

	var r Budget
	for i := 0; i < 20; i++ {
		if !r.Result() {
			t.Fatalf("result %d refused", i+1)
		}
	}
	if r.Result() || r.Stopped() != "Stopped after 20 results." {
		t.Errorf("result 21: stopped = %q", r.Stopped())
	}

	var m Budget
	m.Result()
	big := strings.Repeat("a", 60000)
	ok := true
	n := 0
	for ok && n < 1000 {
		ok = m.Row([]Cell{big, big, big, big, big})
		n++
	}
	if ok || m.Stopped() != "Stopped: the results passed 16 MB." {
		t.Errorf("bytes: ok=%v stopped=%q after %d rows", ok, m.Stopped(), n)
	}
}

func TestBudgetRowsResetPerResult(t *testing.T) {
	var b Budget
	b.Result()
	for i := 0; i < 1000; i++ {
		b.Row([]Cell{"x"})
	}
	if !b.Result() || !b.Row([]Cell{"x"}) {
		t.Fatal("a new result starts its own row count")
	}
}
