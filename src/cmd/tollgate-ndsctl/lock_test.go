package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The flock evidence is cross-process on purpose: flock(2) is what serialises
// two *separate* shim processes, and an in-process mutex cannot be tested this
// way. These two tests use the standard Go re-exec pattern: the parent launches
// several copies of the test binary, each of which takes the lock, logs its
// enter/exit nanoseconds, holds for a fixed window, and exits.
//
// TestLockMutualExclusion asserts the critical sections never overlap. Under
// `-tags nft_noflock` (the negative control) lockGate does nothing, the three
// helpers overlap immediately, and this test FAILS — which is exactly the
// card's "strip flock and the concurrency leg must fail".

const (
	envHelper = "TOLLGATE_LOCK_HELPER"
	envLog    = "TOLLGATE_LOCK_LOG"
	envPath   = "TOLLGATE_LOCK_PATH"
	envHold   = "TOLLGATE_LOCK_HOLD_MS"
)

// TestLockHelperProcess is the subprocess body, not a real assertion. It is a
// no-op unless launched by TestLockMutualExclusion.
func TestLockHelperProcess(t *testing.T) {
	if os.Getenv(envHelper) != "1" {
		t.Skip("lock helper is driven by TestLockMutualExclusion")
	}
	logPath, lockPath := os.Getenv(envLog), os.Getenv(envPath)
	holdMs, _ := strconv.Atoi(os.Getenv(envHold))
	if logPath == "" || lockPath == "" {
		t.Fatal("helper missing TOLLGATE_LOCK_LOG / TOLLGATE_LOCK_PATH")
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	err = lockGate(lockPath, func() error {
		fmt.Fprintf(f, "enter %d\n", time.Now().UnixNano())
		time.Sleep(time.Duration(holdMs) * time.Millisecond)
		fmt.Fprintf(f, "exit %d\n", time.Now().UnixNano())
		return nil
	})
	if err != nil {
		t.Fatalf("helper lockGate: %v", err)
	}
}

type lockEvent struct {
	ts   int64
	open bool
}

func TestLockMutualExclusion(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "ndsctl.lock")
	logPath := filepath.Join(dir, "events.log")

	const procs = 3
	cmds := make([]*exec.Cmd, 0, procs)
	for i := 0; i < procs; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=TestLockHelperProcess", "-test.timeout=60s")
		cmd.Env = append(os.Environ(),
			envHelper+"=1",
			envLog+"="+logPath,
			envPath+"="+lockPath,
			envHold+"=150",
		)
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatalf("start helper %d: %v", i, err)
		}
		cmds = append(cmds, cmd)
	}
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("helper %d exited: %v", i, err)
		}
	}

	events, err := readLockEvents(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2*procs {
		t.Fatalf("logged %d events, want %d (3 enter + 3 exit)", len(events), 2*procs)
	}
	peak := 0
	cur := 0
	for _, e := range events {
		if e.open {
			cur++
			if cur > peak {
				peak = cur
			}
		} else {
			cur--
		}
	}
	if peak != 1 {
		t.Fatalf("peak concurrent holders = %d, want 1: the flock is not serialising (this is the expected failure under -tags nft_noflock)", peak)
	}
}

func readLockEvents(path string) ([]lockEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var events []lockEvent
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 {
			continue
		}
		ts, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		events = append(events, lockEvent{ts: ts, open: fields[0] == "enter"})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].ts < events[j].ts })
	return events, sc.Err()
}
