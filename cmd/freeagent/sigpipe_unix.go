//go:build !windows

package main

import (
	"os/signal"
	"syscall"
)

// Ignore SIGPIPE so a closed stdout becomes an ordinary write error. The CLI
// can then preserve its structured outcome classification, including after a
// mutation that FreeAgent already accepted.
func ignoreSIGPIPE() {
	signal.Ignore(syscall.SIGPIPE)
}
