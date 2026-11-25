package fips_exit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestObservedMetadata(t *testing.T) {
	for _, tt := range []struct{ schema, body string }{{linkSchema, `{"linkmode":"DEFAULT"}`}, {addressSchema, `{"metric":600,"stable-privacy":true}`}} {
		var row observation
		if err := json.Unmarshal([]byte(tt.body), &row); err != nil {
			t.Fatal(err)
		}
		if !validObservation(row, tt.schema) {
			t.Fatal(tt.body)
		}
	}
	for _, body := range []string{`{"metric":"600"}`, `{"stable-privacy":"true"}`, `{"unknown-control":true}`} {
		var row observation
		_ = json.Unmarshal([]byte(body), &row)
		if validObservation(row, addressSchema) {
			t.Fatal(body)
		}
	}
}

func TestRunnerHelper(t *testing.T) {
	mode := os.Getenv("FIPS_RUNNER_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "valid":
		fmt.Print("[]")
	case "nonzero":
		fmt.Fprint(os.Stderr, "failed")
		os.Exit(7)
	case "stdout":
		fmt.Print(strings.Repeat("x", 65536))
		time.Sleep(time.Minute)
	case "stderr":
		fmt.Fprint(os.Stderr, strings.Repeat("x", 65536))
		time.Sleep(time.Minute)
	case "combined":
		fmt.Print(strings.Repeat("x", 700))
		fmt.Fprint(os.Stderr, strings.Repeat("x", 700))
		time.Sleep(time.Minute)
	case "child":
		child := exec.Command(os.Args[0], "-test.run=^TestRunnerHelper$")
		child.Env = append(os.Environ(), "FIPS_RUNNER_HELPER=hang")
		if err := child.Start(); err != nil {
			os.Exit(8)
		}
		_ = os.WriteFile(os.Getenv("FIPS_CHILD_PID"), []byte(fmt.Sprint(child.Process.Pid)), 0600)
		_ = child.Wait()
	case "hang":
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}
func TestRunnerChildCancellation(t *testing.T) {
	pidfile := t.TempDir() + "/pid"
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRunnerHelper$")
	cmd.Env = append(os.Environ(), "FIPS_RUNNER_HELPER=child", "FIPS_CHILD_PID="+pidfile, "GORACE=atexit_sleep_ms=0")
	_, err := runBounded(ctx, cmd, 1024)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	pid, err := os.ReadFile(pidfile)
	if err != nil {
		t.Fatal(err)
	}
	// An orphan may briefly be a zombie awaiting init; it must not be running.
	stat, err := os.ReadFile("/proc/" + string(pid) + "/stat")
	if err == nil && !strings.Contains(string(stat), ") Z ") {
		t.Fatalf("live child: %s", stat)
	}
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() && cmd.ProcessState.String() != "signal: killed" {
		t.Fatal("parent not waited")
	}
}

func TestBoundedProcess(t *testing.T) {
	for _, mode := range []string{"valid", "nonzero", "stdout", "stderr", "combined", "hang"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRunnerHelper$")
			cmd.Env = append(os.Environ(), "FIPS_RUNNER_HELPER="+mode, "GORACE=atexit_sleep_ms=0")
			start := time.Now()
			b, err := runBounded(ctx, cmd, 1024)
			if time.Since(start) > 2*time.Second {
				t.Fatal("unbounded execution")
			}
			if mode == "valid" {
				if err != nil || string(b) != "[]" {
					t.Fatalf("%q %v", b, err)
				}
			} else if err == nil || b != nil {
				t.Fatalf("failure returned data: %q %v", b, err)
			}
			if mode == "hang" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			if mode == "stdout" || mode == "stderr" || mode == "combined" {
				if !errors.Is(err, ErrProbeOutputLimit) {
					t.Fatal(err)
				}
			}
		})
	}
}
func TestRunnerAllowlist(t *testing.T) {
	good := [][]string{{"-j", "link", "show"}, {"-j", "address", "show"}, {"-j", "-4", "rule", "show"}, {"-j", "-4", "route", "show", "table", "all"}, {"-j", "-6", "route", "get", "fd00::1", "from", "fd00::2"}}
	for _, a := range good {
		if !allowedProbe(Probe{"ip", a, 1024}) {
			t.Fatal(a)
		}
	}
	bad := []Probe{{"sh", good[0], 1024}, {"/usr/bin/ip", good[0], 1024}, {"ip", []string{"route", "add", "default"}, 1024}, {"ip", []string{"-j", "link", "del", "lo"}, 1024}, {"ip", append(good[0], ";id"), 1024}, {"ip", good[0], 0}, {"ip", good[0], (1 << 20) + 1}, {"ip", []string{"-j", "-6", "route", "get", "::1;id", "from", "::2"}, 1024}}
	for _, p := range bad {
		if allowedProbe(p) {
			t.Fatal(p)
		}
		if b, err := RunIPProbe(context.Background(), p); err == nil || b != nil {
			t.Fatal("accepted invalid probe")
		}
	}
}
func TestRunnerCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := RunIPProbe(ctx, Probe{"ip", []string{"-j", "link", "show"}, 1024})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestCollectLocalDisabled(t *testing.T) {
	if _, err := CollectLocalLinkRoutes(context.Background(), Config{}, time.Second); err != nil {
		t.Fatal(err)
	}
}
