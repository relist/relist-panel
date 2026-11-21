package dbquery

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

// SplitRedisArgs splits a console line the way redis-cli does (sdssplitargs):
// whitespace separates, "..." takes \n \r \t \b \a \\ \" and \xHH, '...' takes
// only \', and a closing quote must be followed by a space or the end.
func SplitRedisArgs(line string) ([]string, error) {
	args := []string{}
	isSpace := func(c byte) bool {
		return c == ' ' || c == '\n' || c == '\r' || c == '\t' || c == '\v' || c == '\f' || c == 0
	}
	hex := func(c byte) (byte, bool) {
		switch {
		case c >= '0' && c <= '9':
			return c - '0', true
		case c >= 'a' && c <= 'f':
			return c - 'a' + 10, true
		case c >= 'A' && c <= 'F':
			return c - 'A' + 10, true
		}
		return 0, false
	}
	i := 0
	for {
		for i < len(line) && isSpace(line[i]) {
			i++
		}
		if i >= len(line) {
			return args, nil
		}
		var tok []byte
		inq, insq := false, false
	token:
		for {
			switch {
			case inq:
				if i >= len(line) {
					return nil, errors.New("unbalanced quotes: a double quote is not closed")
				}
				c := line[i]
				switch {
				case c == '\\' && i+3 < len(line) && line[i+1] == 'x' && func() bool { _, a := hex(line[i+2]); _, b := hex(line[i+3]); return a && b }():
					hi, _ := hex(line[i+2])
					lo, _ := hex(line[i+3])
					tok = append(tok, hi<<4|lo)
					i += 3
				case c == '\\' && i+1 < len(line):
					i++
					switch line[i] {
					case 'n':
						tok = append(tok, '\n')
					case 'r':
						tok = append(tok, '\r')
					case 't':
						tok = append(tok, '\t')
					case 'b':
						tok = append(tok, '\b')
					case 'a':
						tok = append(tok, 7)
					default:
						tok = append(tok, line[i])
					}
				case c == '"':
					if i+1 < len(line) && !isSpace(line[i+1]) {
						return nil, errors.New("unbalanced quotes: a closing quote must be followed by a space")
					}
					i++
					break token
				default:
					tok = append(tok, c)
				}
			case insq:
				if i >= len(line) {
					return nil, errors.New("unbalanced quotes: a single quote is not closed")
				}
				c := line[i]
				switch {
				case c == '\\' && i+1 < len(line) && line[i+1] == '\'':
					i++
					tok = append(tok, '\'')
				case c == '\'':
					if i+1 < len(line) && !isSpace(line[i+1]) {
						return nil, errors.New("unbalanced quotes: a closing quote must be followed by a space")
					}
					i++
					break token
				default:
					tok = append(tok, c)
				}
			default:
				if i >= len(line) {
					break token
				}
				switch c := line[i]; {
				case isSpace(c):
					i++
					break token
				case c == '"':
					inq = true
				case c == '\'':
					insq = true
				default:
					tok = append(tok, c)
				}
			}
			i++
		}
		args = append(args, string(tok))
	}
}

// CommandInfo is what COMMAND INFO <name> answers: whether the server knows
// the command, its flags, and its subcommands' flags by lower-case name.
type CommandInfo struct {
	Known       bool
	Flags       []string
	Subcommands map[string][]string
}

func flagList(r Reply) []string {
	list, _ := r.Value.([]Reply)
	out := []string{}
	for _, f := range list {
		if s, ok := f.Value.(string); ok {
			out = append(out, strings.ToLower(s))
		}
	}
	return out
}

// ParseCommandInfo reads the reply of COMMAND INFO <name>: an array holding
// the command's entry, or nil for a command the server does not know. An
// entry is [name, arity, flags, first, last, step, categories, tips, key
// specs, subcommands].
func ParseCommandInfo(r Reply) CommandInfo {
	outer, _ := r.Value.([]Reply)
	if r.Type != "array" || len(outer) != 1 || outer[0].Type != "array" {
		return CommandInfo{}
	}
	entry, _ := outer[0].Value.([]Reply)
	if len(entry) < 3 {
		return CommandInfo{}
	}
	info := CommandInfo{Known: true, Flags: flagList(entry[2])}
	if len(entry) >= 10 {
		subs, _ := entry[9].Value.([]Reply)
		for _, s := range subs {
			row, _ := s.Value.([]Reply)
			if len(row) < 3 {
				continue
			}
			full, _ := row[0].Value.(string)
			_, name, found := strings.Cut(strings.ToLower(full), "|")
			if !found {
				continue
			}
			if info.Subcommands == nil {
				info.Subcommands = map[string][]string{}
			}
			info.Subcommands[name] = flagList(row[2])
		}
	}
	return info
}

// Class is how the console treats a command.
type Class struct {
	Refused, Dangerous bool
	Reason             string
}

// Never in the console, writes or not: they change the connection's own state
// or stream, which one request and one reply cannot carry.
var neverCommands = map[string]bool{
	"SUBSCRIBE": true, "PSUBSCRIBE": true, "SSUBSCRIBE": true, "UNSUBSCRIBE": true, "PUNSUBSCRIBE": true, "SUNSUBSCRIBE": true,
	"MONITOR": true, "SYNC": true, "PSYNC": true, "MULTI": true, "EXEC": true, "DISCARD": true, "WATCH": true, "UNWATCH": true,
	"SELECT": true, "AUTH": true, "HELLO": true, "RESET": true, "QUIT": true,
	// Scripts and modules, for everyone: a script is a built-up command, so
	// it gets around the classifier (EVAL "redis.call(\'FLUSHALL\')").
	"EVAL": true, "EVALSHA": true, "EVAL_RO": true, "EVALSHA_RO": true,
	"FCALL": true, "FCALL_RO": true, "FUNCTION": true, "SCRIPT": true, "MODULE": true,
}

// Need a second confirmation even with writes on.
var dangerous = map[string]bool{
	"FLUSHALL": true, "FLUSHDB": true, "SHUTDOWN": true, "DEBUG": true, "SWAPDB": true, "REPLICAOF": true, "SLAVEOF": true, "FAILOVER": true,
	"CONFIG SET": true, "ACL SETUSER": true, "ACL DELUSER": true,
}

// Run read-only although no flag says readonly.
var readOnlyAllowed = map[string]bool{
	"PING": true, "ECHO": true, "INFO": true, "TIME": true, "COMMAND": true, "CONFIG GET": true, "CLIENT LIST": true, "CLIENT INFO": true,
	"SLOWLOG GET": true, "SLOWLOG LEN": true, "MEMORY USAGE": true, "MEMORY STATS": true, "LATENCY LATEST": true,
}

// Classify decides whether args may run. info is COMMAND INFO of args[0].
func Classify(info CommandInfo, args []string, writes bool) Class {
	if len(args) == 0 {
		return Class{Refused: true, Reason: "there is nothing to run"}
	}
	cmd := strings.ToUpper(args[0])
	if neverCommands[cmd] || (cmd == "CLIENT" && len(args) > 1 && strings.EqualFold(args[1], "REPLY")) {
		name := cmd
		if cmd == "CLIENT" {
			name = "CLIENT REPLY"
		}
		reason := name + " is not available in the console"
		if cmd == "SELECT" {
			reason += ": use the database selector"
		}
		return Class{Refused: true, Reason: reason}
	}
	if !info.Known {
		return Class{Refused: true, Reason: "unknown command"}
	}
	flags, sub := info.Flags, ""
	if len(info.Subcommands) > 0 {
		if len(args) < 2 {
			return Class{Refused: true, Reason: cmd + " needs a subcommand"}
		}
		sub = strings.ToLower(args[1])
		f, ok := info.Subcommands[sub]
		if !ok {
			return Class{Refused: true, Reason: "unknown subcommand " + strings.ToUpper(sub) + " of " + cmd}
		}
		flags = f
	}
	full := cmd
	if sub != "" {
		full = cmd + " " + strings.ToUpper(sub)
	}
	out := Class{Dangerous: dangerous[cmd] || dangerous[full]}
	if writes {
		return out
	}
	for _, f := range flags {
		if f == "readonly" {
			return out
		}
	}
	if readOnlyAllowed[cmd] || readOnlyAllowed[full] {
		return out
	}
	out.Refused, out.Reason = true, "writes are off"
	return out
}

// RedisResponse is a console run's outcome. Like every run it is data, never
// an HTTP error, so the audit middleware records it.
type RedisResponse struct {
	Reply        *Reply `json:"reply"`
	Refused      string `json:"refused,omitempty"`
	NeedsConfirm bool   `json:"needsConfirm,omitempty"`
	Error        string `json:"error,omitempty"`
	// Cancelled: the request ended, or the batch passed 30 s.
	Cancelled  bool  `json:"cancelled,omitempty"`
	DurationMs int64 `json:"durationMs"`
}

// KeyInfo is one key of a scan.
type KeyInfo struct {
	Key   string `json:"key"`
	Type  string `json:"type"`
	TTLms int64  `json:"ttlMs"` // -1: no expiry, -2: gone
}

// KeyItem is one element of a key's value: a string's value, a hash field and
// value, a list or set member, or a sorted set member and score.
type KeyItem struct {
	Field string `json:"field,omitempty"`
	Value string `json:"value"`
	Score string `json:"score,omitempty"`
	// Bytes is the full size of a value that was cut.
	Bytes int `json:"bytes,omitempty"`
}

// KeyValue is a key's value by type, at most 1,000 elements.
type KeyValue struct {
	Key         string    `json:"key"`
	Type        string    `json:"type"`
	TTLms       int64     `json:"ttlMs"`
	Total       int64     `json:"total"`
	More        bool      `json:"more"`
	Unsupported bool      `json:"unsupported,omitempty"`
	Items       []KeyItem `json:"items"`
}

// redisFail turns a connection error into the response: Cancelled when the
// context ended, the error's words otherwise (never the password).
func redisFail(resp *RedisResponse, ctx, parent context.Context, err error) {
	switch {
	case parent.Err() != nil:
		resp.Cancelled, resp.Error = true, msgCancelled
	case ctx.Err() != nil:
		resp.Cancelled, resp.Error = true, msgTimedOut
	default:
		resp.Error = err.Error()
	}
}

// RunRedis runs one console line on database db. It sends nothing the
// classifier refuses, and nothing dangerous before confirmDangerous.
func RunRedis(parent context.Context, t Target, db int, line string, writes, confirmDangerous bool) RedisResponse {
	began := time.Now()
	resp := RedisResponse{}
	defer func() { resp.DurationMs = sinceMs(began) }()
	args, err := SplitRedisArgs(line)
	if err != nil {
		resp.Error = err.Error()
		return resp
	}
	if len(args) == 0 {
		resp.Refused = "there is nothing to run"
		return resp
	}
	ctx, cancel := context.WithTimeout(parent, runTimeout)
	defer cancel()
	c, err := dialRedis(ctx, t, db)
	if err != nil {
		redisFail(&resp, ctx, parent, err)
		return resp
	}
	defer c.close()
	infoReply, err := c.Do("COMMAND", "INFO", args[0])
	if err != nil {
		redisFail(&resp, ctx, parent, err)
		return resp
	}
	class := Classify(ParseCommandInfo(infoReply), args, writes)
	if class.Refused {
		resp.Refused = class.Reason
		return resp
	}
	if class.Dangerous && !confirmDangerous {
		resp.NeedsConfirm = true
		return resp
	}
	reply, err := c.Do(args...)
	if err != nil {
		redisFail(&resp, ctx, parent, err)
		return resp
	}
	resp.Reply = &reply
	return resp
}

func replyString(r Reply) string {
	s, _ := r.Value.(string)
	return s
}

func replyInt(r Reply) int64 {
	n, _ := r.Value.(int64)
	return n
}

func redisErr(r Reply) error {
	if r.Type == "error" {
		return errors.New(replyString(r))
	}
	return nil
}

// ScanKeys reads one SCAN page (COUNT 100) of database db with each key's
// type and TTL. next is "0" when the scan is complete.
func ScanKeys(parent context.Context, t Target, db int, pattern, cursor string) ([]KeyInfo, string, error) {
	if pattern == "" {
		pattern = "*"
	}
	if cursor == "" {
		cursor = "0"
	}
	ctx, cancel := context.WithTimeout(parent, runTimeout)
	defer cancel()
	c, err := dialRedis(ctx, t, db)
	if err != nil {
		return nil, "", err
	}
	defer c.close()
	r, err := c.Do("SCAN", cursor, "MATCH", pattern, "COUNT", "100")
	if err != nil {
		return nil, "", err
	}
	if err := redisErr(r); err != nil {
		return nil, "", err
	}
	parts, _ := r.Value.([]Reply)
	if len(parts) != 2 {
		return nil, "", errors.New("unexpected SCAN reply")
	}
	names, _ := parts[1].Value.([]Reply)
	keys := make([]KeyInfo, 0, len(names))
	for _, n := range names {
		key := replyString(n)
		tr, err := c.Do("TYPE", key)
		if err != nil {
			return nil, "", err
		}
		pr, err := c.Do("PTTL", key)
		if err != nil {
			return nil, "", err
		}
		keys = append(keys, KeyInfo{Key: key, Type: replyString(tr), TTLms: replyInt(pr)})
	}
	return keys, replyString(parts[0]), nil
}

func item(r Reply) KeyItem { return KeyItem{Value: replyString(r), Bytes: r.Bytes} }

// ReadKey reads the value of key by its type, at most 1,000 elements: a
// string with GET, a hash with one HSCAN, a list with LRANGE, a set with one
// SSCAN and a sorted set with ZRANGE. Another type is only named.
func ReadKey(parent context.Context, t Target, db int, key string) (KeyValue, error) {
	ctx, cancel := context.WithTimeout(parent, runTimeout)
	defer cancel()
	c, err := dialRedis(ctx, t, db)
	if err != nil {
		return KeyValue{}, err
	}
	defer c.close()
	kv := KeyValue{Key: key, Items: []KeyItem{}}
	tr, err := c.Do("TYPE", key)
	if err != nil {
		return kv, err
	}
	kv.Type = replyString(tr)
	if pr, err := c.Do("PTTL", key); err == nil {
		kv.TTLms = replyInt(pr)
	} else {
		return kv, err
	}
	// A hash's fields and values, or a sorted set's members and scores, come
	// in one flat array: twice the elements.
	c.elemCap = 2 * redisMaxElems
	switch kv.Type {
	case "string":
		r, err := c.Do("GET", key)
		if err != nil {
			return kv, err
		}
		if r.Type != "nil" {
			kv.Items, kv.Total = append(kv.Items, item(r)), 1
		}
	case "hash", "set":
		scan, count := "HSCAN", "HLEN"
		if kv.Type == "set" {
			scan, count = "SSCAN", "SCARD"
		}
		r, err := c.Do(scan, key, "0", "COUNT", strconv.Itoa(redisMaxElems))
		if err != nil {
			return kv, err
		}
		parts, _ := r.Value.([]Reply)
		if len(parts) == 2 {
			flat, _ := parts[1].Value.([]Reply)
			kv.More = replyString(parts[0]) != "0" || parts[1].More > 0
			if kv.Type == "hash" {
				for i := 0; i+1 < len(flat); i += 2 {
					it := item(flat[i+1])
					it.Field = replyString(flat[i])
					kv.Items = append(kv.Items, it)
				}
			} else {
				for _, m := range flat {
					kv.Items = append(kv.Items, item(m))
				}
			}
		}
		n, err := c.Do(count, key)
		if err != nil {
			return kv, err
		}
		kv.Total = replyInt(n)
		// COUNT is only a hint: a page can hold more than the 1,000 shown.
		if len(kv.Items) > redisMaxElems {
			kv.Items = kv.Items[:redisMaxElems]
		}
		kv.More = kv.More || kv.Total > int64(len(kv.Items))
	case "list", "zset":
		var r, n Reply
		if kv.Type == "list" {
			r, err = c.Do("LRANGE", key, "0", strconv.Itoa(redisMaxElems-1))
		} else {
			r, err = c.Do("ZRANGE", key, "0", strconv.Itoa(redisMaxElems-1), "WITHSCORES")
		}
		if err != nil {
			return kv, err
		}
		flat, _ := r.Value.([]Reply)
		if kv.Type == "list" {
			for _, m := range flat {
				kv.Items = append(kv.Items, item(m))
			}
			n, err = c.Do("LLEN", key)
		} else {
			for i := 0; i+1 < len(flat); i += 2 {
				it := item(flat[i])
				it.Score = replyString(flat[i+1])
				kv.Items = append(kv.Items, it)
			}
			n, err = c.Do("ZCARD", key)
		}
		if err != nil {
			return kv, err
		}
		kv.Total = replyInt(n)
		kv.More = kv.Total > int64(len(kv.Items))
	case "none":
		kv.Total = 0
	default:
		kv.Unsupported = true
	}
	return kv, nil
}
