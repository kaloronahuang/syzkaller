// Copyright 2021 syzkaller project authors. All rights reserved.
// Use of this source code is governed by Apache 2 LICENSE that can be found in the LICENSE file.

package main

import (
	"math/rand"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/syzkaller/pkg/ipc"
	"github.com/google/syzkaller/pkg/osutil"
	"github.com/google/syzkaller/prog"
)

func createTestServer(t *testing.T) *RPCServer {
	target, err := prog.GetTarget("test", "64")
	if err != nil {
		t.Fatalf("failed to initialise test target: %v", err)
	}
	vrf := Verifier{
		target:      target,
		choiceTable: target.DefaultChoiceTable(),
		rnd:         rand.New(rand.NewSource(time.Now().UnixNano())),
		progIdx:     3,
	}
	vrf.resultsdir = makeTestResultDirectory(t)
	vrf.stats = emptyTestStats()
	srv, err := startRPCServer(&vrf)
	if err != nil {
		t.Fatalf("failed to initialise RPC server: %v", err)
	}
	return srv
}

func getTestProgram(t *testing.T) *prog.Prog {
	p := "breaks_returns()\n" +
		"minimize$0(0x1, 0x1)\n" +
		"test$res0()\n"
	target := prog.InitTargetTest(t, "test", "64")
	prog, err := target.Deserialize([]byte(p), prog.Strict)
	if err != nil {
		t.Fatalf("failed to deserialise test program: %v", err)
	}
	return prog
}

func makeTestResultDirectory(t *testing.T) string {
	resultsdir := "test"
	err := osutil.MkdirAll(resultsdir)
	if err != nil {
		t.Fatalf("failed to create results directory: %v", err)
	}
	resultsdir, err = filepath.Abs(resultsdir)
	if err != nil {
		t.Fatalf("failed to get absolute path of resultsdir: %v", err)
	}
	return resultsdir
}

func makeResult(pool int, errnos []int, flags ...int) *Result {
	r := &Result{Pool: pool, Info: ipc.ProgInfo{Calls: []ipc.CallInfo{}}}
	for _, e := range errnos {
		r.Info.Calls = append(r.Info.Calls, ipc.CallInfo{Errno: e})
	}

	for idx, f := range flags {
		r.Info.Calls[idx].Flags = ipc.CallFlags(f)
	}
	return r
}

func emptyTestStats() *Stats {
	return &Stats{
		Calls: map[string]*CallStats{
			"breaks_returns": {Name: "breaks_returns", States: map[int]bool{}},
			"minimize$0":     {Name: "minimize$0", States: map[int]bool{}},
			"test$res0":      {Name: "test$res0", States: map[int]bool{}},
		},
	}
}

func makeCallStats(name string, occurrences, mismatches int, states map[int]bool) *CallStats {
	return &CallStats{Name: name,
		Occurrences: occurrences,
		Mismatches:  mismatches,
		States:      states}
}