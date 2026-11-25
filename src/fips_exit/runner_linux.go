package fips_exit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"
)

// ErrProbeOutputLimit means capture was stopped; partial data is never evidence.
var ErrProbeOutputLimit = errors.New("probe output limit exceeded")

func allowedProbe(p Probe) bool {
	if p.Program != "ip" || p.MaxOutputBytes <= 0 || p.MaxOutputBytes > 1<<20 {
		return false
	}
	for _, a := range [][]string{{"-j", "link", "show"}, {"-j", "address", "show"}, {"-j", "-4", "rule", "show"}, {"-j", "-4", "route", "show", "table", "all"}} {
		if slices.Equal(p.Args, a) {
			return true
		}
	}
	a := p.Args
	if len(a) != 7 || !slices.Equal(a[:4], []string{"-j", "-6", "route", "get"}) || a[5] != "from" {
		return false
	}
	for _, s := range []string{a[4], a[6]} {
		ip, err := netip.ParseAddr(s)
		if err != nil || !ip.Is6() || ip.Is4In6() || ip.Zone() != "" {
			return false
		}
	}
	return true
}

// trustedIP ignores PATH, resolves symlinks and requires a root-owned executable
// and root-owned, non-group/world-writable ancestors. No caller-supplied binary.
func trustedIP() (string, error) {
	for _, candidate := range []string{"/usr/sbin/ip", "/usr/bin/ip", "/sbin/ip", "/bin/ip"} {
		path, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		valid := true
		for p := path; ; p = filepath.Dir(p) {
			st, err := os.Stat(p)
			if err != nil {
				valid = false
				break
			}
			owner, ok := st.Sys().(*syscall.Stat_t)
			if !ok || owner.Uid != 0 || st.Mode().Perm()&0022 != 0 || (p == path && (!st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0)) {
				valid = false
				break
			}
			if p == "/" {
				break
			}
		}
		if valid {
			return path, nil
		}
	}
	return "", fmt.Errorf("no trusted root-owned ip executable")
}

// RunIPProbe executes only the observer's read-only argv forms, without a shell.
// It always imposes a 30-second ceiling in addition to the caller's deadline.
func RunIPProbe(ctx context.Context, p Probe) ([]byte, error) {
	if !allowedProbe(p) {
		return nil, fmt.Errorf("probe not allowed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := trustedIP()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, p.Args...)
	cmd.Env = []string{"LC_ALL=C", "LANG=C"}
	return runBounded(ctx, cmd, p.MaxOutputBytes)
}

type probeCapture struct {
	mu          sync.Mutex
	out, errout bytes.Buffer
	remaining   int
	exceeded    bool
	stop        func()
}
type probeWriter struct {
	capture *probeCapture
	stderr  bool
}

func (w probeWriter) Write(p []byte) (int, error) {
	c := w.capture
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(p) > c.remaining {
		c.exceeded = true
		c.stop()
		return 0, ErrProbeOutputLimit
	}
	c.remaining -= len(p)
	if w.stderr {
		return c.errout.Write(p)
	}
	return c.out.Write(p)
}

// Linux process groups terminate children too; Wait reaps the direct process.
// WaitDelay bounds inherited-pipe cleanup even if an unexpected child holds it.
func runBounded(ctx context.Context, cmd *exec.Cmd, limit int) ([]byte, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	kill := func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.Cancel = kill
	cmd.WaitDelay = 100 * time.Millisecond
	capture := &probeCapture{remaining: limit, stop: func() { _ = kill() }}
	cmd.Stdout = probeWriter{capture, false}
	cmd.Stderr = probeWriter{capture, true}
	err := cmd.Run()
	// Trusted ip does not fork; kill any unexpected surviving group members.
	if cmd.Process != nil {
		_ = kill()
	}
	if capture.exceeded {
		return nil, ErrProbeOutputLimit
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("ip probe failed: %w (stderr: %s)", err, capture.errout.String())
	}
	return capture.out.Bytes(), nil
}

// CollectLocalLinkRoutes is opt-in package functionality, not daemon activation.
func CollectLocalLinkRoutes(ctx context.Context, c Config, timeout time.Duration) (LinkRouteEvidence, error) {
	return CollectLinkRoutes(ctx, c, RunIPProbe, timeout)
}
