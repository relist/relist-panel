// Package safely runs background work so that a panic in it is logged and
// contained instead of taking the whole panel down: net/http recovers a
// panicking handler goroutine, but nothing recovers a goroutine the panel
// started itself.
package safely

import (
	"fmt"
	"log"
	"runtime/debug"
)

// Do runs fn and recovers a panic, logging it with its stack. The caller
// carries on as if fn had returned.
func Do(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("%s panicked: %v\n%s", name, r, debug.Stack())
		}
	}()
	fn()
}

// Call runs fn and returns its error; a panic becomes an error naming the job,
// so it flows into the caller's existing failure path. The stack is logged.
func Call(name string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("%s panicked: %v\n%s", name, r, debug.Stack())
			err = fmt.Errorf("%s panicked: %v", name, r)
		}
	}()
	return fn()
}
