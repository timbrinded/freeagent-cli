//go:build !windows

package main

import (
	"os"
	"os/exec"
	"testing"
)

func TestIgnoreSIGPIPETurnsClosedStdoutIntoWriteError(t *testing.T) {
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := readEnd.Close(); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestSIGPIPEHelperProcess$")
	command.Env = append(os.Environ(), "FREEAGENT_SIGPIPE_HELPER=1")
	command.Stdout = writeEnd
	if err := command.Start(); err != nil {
		_ = writeEnd.Close()
		t.Fatal(err)
	}
	if err := writeEnd.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("helper was terminated instead of receiving a write error: %v", err)
	}
}

func TestSIGPIPEHelperProcess(t *testing.T) {
	if os.Getenv("FREEAGENT_SIGPIPE_HELPER") != "1" {
		return
	}
	ignoreSIGPIPE()
	if _, err := os.Stdout.Write(make([]byte, 64<<10)); err == nil {
		os.Exit(91)
	}
	os.Exit(0)
}
