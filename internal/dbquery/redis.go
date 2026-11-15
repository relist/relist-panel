package dbquery

import (
	"errors"
	"strings"
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
}

// Need a second confirmation even with writes on.
var dangerous = map[string]bool{
	"FLUSHALL": true, "FLUSHDB": true, "SHUTDOWN": true, "DEBUG": true, "SWAPDB": true, "REPLICAOF": true, "SLAVEOF": true, "FAILOVER": true,
	"CONFIG SET": true, "SCRIPT FLUSH": true, "FUNCTION FLUSH": true, "MODULE LOAD": true, "MODULE UNLOAD": true,
	"ACL SETUSER": true, "ACL DELUSER": true,
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
