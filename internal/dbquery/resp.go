package dbquery

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// Limits of one Redis reply: no single reply can grow the panel's memory
// past what a run's result may be.
const (
	redisMaxElems      = 1000     // kept per collection
	redisMaxBulk       = 65536    // kept bytes of one bulk string
	redisMaxTotalElems = 50000    // kept replies in all of one reply tree
	redisMaxDepth      = 16       // nesting of arrays
	respLineMax        = 64 << 10 // one status, error or length line
)

// Reply is a RESP2 reply as JSON. Bytes is a bulk string's full size when it
// was cut; More counts the collection's elements that were read and dropped.
type Reply struct {
	Type  string `json:"type"` // string | error | integer | bulk | nil | array
	Value any    `json:"value"`
	Bytes int    `json:"bytes,omitempty"`
	More  int    `json:"more,omitempty"`
}

// respConn is one RESP2 connection.
type respConn struct {
	conn net.Conn
	br   *bufio.Reader
	stop chan struct{}
}

func newRespConn(c net.Conn) *respConn {
	return &respConn{conn: c, br: bufio.NewReaderSize(c, respLineMax), stop: make(chan struct{})}
}

func (c *respConn) close() {
	select {
	case <-c.stop:
	default:
		close(c.stop)
	}
	_ = c.conn.Close()
}

// dialRedis connects, authenticates with the Database's password (the
// default user) and selects db. The connection closes when ctx ends.
func dialRedis(ctx context.Context, t Target, db int) (*respConn, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	nc, err := d.DialContext(ctx, "tcp", net.JoinHostPort(t.Host, strconv.Itoa(t.Port)))
	if err != nil {
		return nil, err
	}
	c := newRespConn(nc)
	if dl, ok := ctx.Deadline(); ok {
		_ = nc.SetDeadline(dl)
	}
	go func() {
		select {
		case <-ctx.Done():
			_ = nc.Close()
		case <-c.stop:
		}
	}()
	if t.Password != "" {
		r, err := c.Do("AUTH", t.Password)
		if err != nil {
			c.close()
			return nil, err
		}
		if r.Type == "error" {
			c.close()
			return nil, errors.New("authentication was refused")
		}
	}
	if r, err := c.Do("SELECT", strconv.Itoa(db)); err != nil {
		c.close()
		return nil, err
	} else if r.Type == "error" {
		c.close()
		return nil, fmt.Errorf("%v", r.Value)
	}
	return c, nil
}

// Do sends one command and reads its reply, capped as Reply describes.
func (c *respConn) Do(args ...string) (Reply, error) {
	var req bytes.Buffer
	fmt.Fprintf(&req, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&req, "$%d\r\n%s\r\n", len(a), a)
	}
	if _, err := c.conn.Write(req.Bytes()); err != nil {
		return Reply{}, err
	}
	st := &readState{}
	return c.read(st, 0, true)
}

type readState struct{ bytes, elems int }

func (c *respConn) line() (string, error) {
	b, err := c.br.ReadSlice('\n')
	if err != nil {
		if errors.Is(err, bufio.ErrBufferFull) {
			return "", errors.New("the server sent a line that is too long")
		}
		return "", err
	}
	if len(b) < 2 || b[len(b)-2] != '\r' {
		return "", errors.New("malformed reply")
	}
	return string(b[:len(b)-2]), nil
}

func (c *respConn) discard(n int64) error {
	_, err := io.CopyN(io.Discard, c.br, n)
	return err
}

// read parses one reply. With keep false it only consumes the bytes, so a
// reply dropped for a cap leaves nothing on the wire.
func (c *respConn) read(st *readState, depth int, keep bool) (Reply, error) {
	if depth > redisMaxDepth {
		return Reply{}, errors.New("the reply is nested too deeply")
	}
	l, err := c.line()
	if err != nil {
		return Reply{}, err
	}
	if l == "" {
		return Reply{}, errors.New("malformed reply")
	}
	body := l[1:]
	switch l[0] {
	case '+':
		return Reply{Type: "string", Value: body}, nil
	case '-':
		return Reply{Type: "error", Value: body}, nil
	case ':':
		n, err := strconv.ParseInt(body, 10, 64)
		if err != nil {
			return Reply{}, errors.New("malformed integer reply")
		}
		return Reply{Type: "integer", Value: n}, nil
	case '$':
		n, err := strconv.ParseInt(body, 10, 64)
		if err != nil || n < -1 {
			return Reply{}, errors.New("malformed bulk reply")
		}
		if n == -1 {
			return Reply{Type: "nil"}, nil
		}
		take := n
		if !keep || take > redisMaxBulk {
			take = redisMaxBulk
		}
		if !keep || st.bytes+int(take) > maxResultBytes {
			take = 0
		}
		buf := make([]byte, take)
		if _, err := io.ReadFull(c.br, buf); err != nil {
			return Reply{}, err
		}
		if err := c.discard(n - take + 2); err != nil {
			return Reply{}, err
		}
		st.bytes += int(take)
		r := Reply{Type: "bulk", Value: strings.ToValidUTF8(string(buf), "�")}
		if take < n {
			r.Bytes = int(n)
		}
		return r, nil
	case '*':
		n, err := strconv.ParseInt(body, 10, 64)
		if err != nil || n < -1 {
			return Reply{}, errors.New("malformed array reply")
		}
		if n == -1 {
			return Reply{Type: "nil"}, nil
		}
		kept := n
		if kept > redisMaxElems {
			kept = redisMaxElems
		}
		items := []Reply{}
		for i := int64(0); i < n; i++ {
			keepThis := keep && i < kept && st.elems < redisMaxTotalElems
			if keepThis {
				st.elems++
			}
			el, err := c.read(st, depth+1, keepThis)
			if err != nil {
				return Reply{}, err
			}
			if keepThis {
				items = append(items, el)
			}
		}
		return Reply{Type: "array", Value: items, More: int(n) - len(items)}, nil
	}
	return Reply{}, errors.New("malformed reply")
}
