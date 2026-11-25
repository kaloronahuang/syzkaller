// Copyright 2026 syzkaller project authors. All rights reserved.
// Use of this source code is governed by Apache 2 LICENSE that can be found in the LICENSE file.

package instance

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/syzkaller/vm"
)

func TestCReproCommand(t *testing.T) {
	tests := []struct {
		opts    CRunOptions
		command string
		exit    vm.ExitCondition
	}{
		{
			opts:    CRunOptions{},
			command: "/repro",
			exit:    binExitConditions,
		},
		{
			opts: CRunOptions{Loop: true, Heartbeat: 30 * time.Second},
			command: `sh -c '(while :; do echo "executed programs: 0 [C reproducer heartbeat]"; sleep 30; done) & ` +
				`HEARTBEAT=$!; while :; do /repro; echo "C reproducer exited with status $?, restarting"; sleep 1; done'`,
			exit: vm.ExitTimeout,
		},
		{
			opts:    CRunOptions{Loop: true},
			command: `sh -c 'while :; do /repro; echo "C reproducer exited with status $?, restarting"; sleep 1; done'`,
			exit:    vm.ExitTimeout,
		},
		{
			opts: CRunOptions{Heartbeat: 30 * time.Second},
			command: `sh -c '(while :; do echo "executed programs: 0 [C reproducer heartbeat]"; sleep 30; done) & ` +
				`HEARTBEAT=$!; /repro; STATUS=$?; kill $HEARTBEAT; exit $STATUS'`,
			exit: binExitConditions,
		},
	}
	for _, test := range tests {
		command, exit := CReproCommand("/repro", test.opts)
		if command != test.command {
			t.Errorf("%+v: command:\n got: %v\nwant: %v", test.opts, command, test.command)
		}
		if exit != test.exit {
			t.Errorf("%+v: exit conditions: got %v, want %v", test.opts, exit, test.exit)
		}
	}
}

// TestCReproCommandRuns runs the generated command in a local shell, the way the VM's
// shell would, with a one-shot program that exits at once.
func TestCReproCommandRuns(t *testing.T) {
	shell := os.Getenv("CREPRO_TEST_SHELL") // e.g. "busybox sh"; default: sh
	dir := t.TempDir()
	bin := filepath.Join(dir, "oneshot")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho one-shot ran\nexit 3\n"), 0755); err != nil {
		t.Fatal(err)
	}

	command, _ := CReproCommand(bin, CRunOptions{Loop: true, Heartbeat: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 4500*time.Millisecond)
	defer cancel()
	args := []string{"sh", "-c", command}
	if shell != "" {
		args = append(strings.Fields(shell), "-c", command)
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	// the heartbeat runs in the background and keeps the output pipe open, so kill the
	// whole process group on timeout, as tearing down the VM does in a real run;
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	out, _ := cmd.CombinedOutput()
	output := string(out)

	if ctx.Err() == nil {
		t.Fatalf("the looping command exited on its own:\n%s", output)
	}
	if n := strings.Count(output, "one-shot ran"); n < 3 {
		t.Errorf("program ran %v times in 4.5s, want it restarted at least 3 times:\n%s", n, output)
	}
	if n := strings.Count(output, "C reproducer exited with status 3, restarting"); n < 3 {
		t.Errorf("got %v exit-status lines, want at least 3:\n%s", n, output)
	}
	if n := strings.Count(output, "executed programs: 0 [C reproducer heartbeat]"); n < 3 {
		t.Errorf("got %v heartbeat lines in 4.5s at 1s interval, want at least 3:\n%s", n, output)
	}
}
