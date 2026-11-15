// Command relist-proxy is the program of a Direct access's proxy container
// (ADR-0041). It forwards every accepted connection to one fixed target and
// has no other mode:
//
//	relist-proxy serve  --listen :<port> --target <host:port> --deadline <unix> --state /tmp/deadline
//	relist-proxy extend --state /tmp/deadline <unix>
//
// serve exits by itself at the deadline, closing the listener and every open
// connection, so the access ends on time even when the panel is down. extend
// writes a later deadline to the state file, which serve rereads every few
// seconds; no connection is dropped.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"relist-panel/internal/tcpproxy"
)

const (
	maxConns    = 64
	dialTimeout = 10 * time.Second
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: relist-proxy serve|extend ...")
		return 2
	}
	switch args[0] {
	case "serve":
		return serve(args[1:], stderr)
	case "extend":
		return extend(args[1:], stderr)
	}
	fmt.Fprintf(stderr, "unknown command %q\n", args[0])
	return 2
}

func serve(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", "", "address to listen on, for example :5432")
	target := fs.String("target", "", "host:port to forward to")
	deadline := fs.Int64("deadline", 0, "unix time to stop at")
	state := fs.String("state", "/tmp/deadline", "file holding the deadline")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *listen == "" || *target == "" || *deadline == 0 {
		fmt.Fprintln(stderr, "serve needs --listen, --target and --deadline")
		return 2
	}
	end := time.Unix(*deadline, 0)
	if !time.Now().Before(end) {
		return 0
	}
	if err := tcpproxy.WriteDeadline(*state, end); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	dial := func(ctx context.Context) (net.Conn, error) {
		d := net.Dialer{Timeout: dialTimeout}
		return d.DialContext(ctx, "tcp", *target)
	}
	if err := tcpproxy.Serve(ctx, ln, dial, tcpproxy.FileDeadline(*state, end), maxConns); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func extend(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("extend", flag.ContinueOnError)
	fs.SetOutput(stderr)
	state := fs.String("state", "/tmp/deadline", "file holding the deadline")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "extend needs one unix time")
		return 2
	}
	n, err := strconv.ParseInt(fs.Arg(0), 10, 64)
	if err != nil || n <= 0 {
		fmt.Fprintln(stderr, "extend needs a unix time")
		return 2
	}
	if err := tcpproxy.WriteDeadline(*state, time.Unix(n, 0)); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
