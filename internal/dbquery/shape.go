package dbquery

import (
	"encoding/hex"
	"unicode/utf8"
)

// Limits of one run (.scratch/databases/plan-phase-4.md).
const (
	maxRows        = 1000
	maxCell        = 64 * 1024
	maxResults     = 20
	maxResultBytes = 16 << 20
)

// Cell is nil (NULL), a string, or a CutCell. Cells are never JSON numbers,
// so 64-bit integers and numerics keep every digit.
type Cell = any

// CutCell is a value that was cut at maxCell or is not UTF-8. Bytes is its
// true size.
type CutCell struct {
	Text  string `json:"text,omitempty"`
	Hex   string `json:"hex,omitempty"`
	Bytes int    `json:"bytes"`
}

// ShapeCell turns a raw value into a Cell.
func ShapeCell(raw []byte, isNull bool) Cell {
	if isNull {
		return nil
	}
	if !utf8.Valid(raw) {
		n := len(raw)
		if n > maxCell/2 {
			n = maxCell / 2
		}
		return CutCell{Hex: hex.EncodeToString(raw[:n]), Bytes: len(raw)}
	}
	if len(raw) <= maxCell {
		return string(raw)
	}
	cut := maxCell
	for cut > 0 && !utf8.RuneStart(raw[cut]) {
		cut--
	}
	return CutCell{Text: string(raw[:cut]), Bytes: len(raw)}
}

func cellSize(c Cell) int {
	switch v := c.(type) {
	case string:
		return len(v)
	case CutCell:
		return len(v.Text) + len(v.Hex)
	}
	return 0
}

// Budget counts what one batch has returned and says why it stopped.
type Budget struct {
	rows, results, bytes int
	stopped              string
}

// Result starts a result; false once maxResults were started.
func (b *Budget) Result() bool {
	if b.results >= maxResults {
		b.stopped = "Stopped after 20 results."
		return false
	}
	b.results++
	b.rows = 0
	return true
}

// Row counts one row; false when it is row maxRows+1 of the result or the
// batch is past maxResultBytes. A refused row is not kept.
func (b *Budget) Row(cells []Cell) bool {
	if b.rows >= maxRows {
		b.stopped = "Stopped after 1,000 rows."
		return false
	}
	n := 0
	for _, c := range cells {
		n += cellSize(c)
	}
	if b.bytes+n > maxResultBytes {
		b.stopped = "Stopped: the results passed 16 MB."
		return false
	}
	b.rows++
	b.bytes += n
	return true
}

// Stopped is the stop text, "" when the batch was not stopped.
func (b *Budget) Stopped() string { return b.stopped }
