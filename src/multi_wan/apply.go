package multi_wan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	cm "github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

const mwan3ConfigPath = "/etc/config/mwan3"
const marksFragmentPath = "/etc/nftables.d/50-multiwan-marks.nft"

// Runner is the applier's only I/O boundary. Run executes name with literal
// arguments (not through a shell). ReadFile must preserve os.ErrNotExist.
// WriteFile must replace complete file contents and honor mode; production
// runners should use an atomic, durable replacement to survive interrupted writes.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (stdout string, err error)
	ReadFile(ctx context.Context, path string) ([]byte, error)
	WriteFile(ctx context.Context, path string, data []byte, mode os.FileMode) error
}

// Applier installs already-derived policies. The caller must serialize Apply
// and Restore across all writers to these files, and validate the enclosing
// MPTCP mark via Validate before calling Apply (that mark is not in cfg).
// BackupDir must be an absolute, trusted, operator-owned directory. Now defaults
// to time.Now. No router runner or background reconciliation is started here.
type Applier struct {
	Run       Runner
	BackupDir string
	Now       func() time.Time
}

// Result is also returned on error so callers retain the recovery handle.
// Changed means the new mwan3 file was written, not that all commands succeeded.
// MWAN3Restarted means its restart command succeeded, not that it became ready.
type Result struct {
	Changed           bool
	BackupPath        string
	MWAN3Restarted    bool
	MarksFragmentPath string
	MarksBackupPath   string
}

// Apply validates and renders both artifacts before I/O. Byte-identical mwan3
// text is a strict no-op, including when the marks fragment is missing/stale.
// Errors stop subsequent operations; they do not automatically roll back.
func (a *Applier) Apply(ctx context.Context, cfg *cm.MultiWANConfig, objectives []Outcome, clients []ClientRoute) (Result, error) {
	result := Result{MarksFragmentPath: marksFragmentPath}
	if err := Validate(cfg, ""); err != nil {
		return result, err
	}
	if cfg == nil || !cfg.Enabled {
		return result, fmt.Errorf("multi_wan: applying requires enabled configuration")
	}
	mwan3, err := RenderMWAN3(cfg, objectives, clients)
	if err != nil {
		return result, err
	}
	marks, err := RenderMarks(cfg, objectives)
	if err != nil {
		return result, err
	}
	if a.Run == nil {
		return result, fmt.Errorf("multi_wan: runner is required")
	}
	current, err := a.Run.ReadFile(ctx, mwan3ConfigPath)
	if err != nil {
		return result, fmt.Errorf("read current mwan3: %w", err)
	}
	if bytes.Equal(current, []byte(mwan3)) {
		return result, nil
	}
	if !filepath.IsAbs(a.BackupDir) || filepath.Clean(a.BackupDir) == "/" {
		return result, fmt.Errorf("multi_wan: BackupDir must be a non-root absolute path")
	}
	if _, err := a.Run.Run(ctx, "mkdir", "-p", "-m", "0700", a.BackupDir); err != nil {
		return result, fmt.Errorf("create backup directory: %w", err)
	}
	// mkdir -p does not change permissions on an existing directory.
	if _, err := a.Run.Run(ctx, "chmod", "0700", a.BackupDir); err != nil {
		return result, fmt.Errorf("secure backup directory: %w", err)
	}
	now := a.Now
	if now == nil {
		now = time.Now
	}
	backup := filepath.Join(a.BackupDir, "mwan3."+now().UTC().Format("20060102T150405.000000000Z")+".bak")
	if _, err := a.Run.ReadFile(ctx, backup); err == nil {
		return result, fmt.Errorf("multi_wan: backup already exists: %s", backup)
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, fmt.Errorf("check backup path: %w", err)
	}
	if err := a.Run.WriteFile(ctx, backup, current, 0600); err != nil {
		return result, fmt.Errorf("backup mwan3: %w", err)
	}
	result.BackupPath = backup
	if err := a.Run.WriteFile(ctx, mwan3ConfigPath, []byte(mwan3), 0600); err != nil {
		return result, fmt.Errorf("write mwan3: %w", err)
	}
	result.Changed = true
	if err := a.commitAndRestart(ctx); err != nil {
		return result, err
	}
	result.MWAN3Restarted = true
	currentMarks, err := a.Run.ReadFile(ctx, marksFragmentPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return result, fmt.Errorf("read marks fragment: %w", err)
	}
	if err == nil && bytes.Equal(currentMarks, []byte(marks)) {
		return result, nil
	}
	// Back up any pre-existing fragment too: a partial apply must be recoverable
	// in both directions, not just for /etc/config/mwan3.
	if err == nil {
		marksBackup := a.backupFileName("marks")
		if werr := a.Run.WriteFile(ctx, marksBackup, currentMarks, 0600); werr != nil {
			return result, fmt.Errorf("backup marks fragment: %w", werr)
		}
		result.MarksBackupPath = marksBackup
	}
	if err := a.Run.WriteFile(ctx, marksFragmentPath, []byte(marks), 0644); err != nil {
		return result, fmt.Errorf("write marks fragment: %w", err)
	}
	// fw4 reload procd-restarts nodogsplash and erases runtime trust/auth state.
	// The caller must maintain the keepalive contract to restore that state.
	if _, err := a.Run.Run(ctx, "fw4", "reload"); err != nil {
		return result, fmt.Errorf("reload firewall: %w", err)
	}
	return result, nil
}

func (a *Applier) backupFileName(kind string) string {
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	return filepath.Join(a.BackupDir, kind+"."+now().UTC().Format("20060102T150405.000000000Z")+".bak")
}

func (a *Applier) commitAndRestart(ctx context.Context) error {
	if _, err := a.Run.Run(ctx, "uci", "commit", "mwan3"); err != nil {
		return fmt.Errorf("commit mwan3: %w", err)
	}
	if _, err := a.Run.Run(ctx, "/etc/init.d/mwan3", "restart"); err != nil {
		return fmt.Errorf("restart mwan3: %w", err)
	}
	return nil
}

// Restore reinstalls the mwan3 backup and restarts mwan3. When marksBackupPath
// is non-empty the previous nft fragment is also reinstalled and fw4 reloaded.
// A partial apply is therefore recoverable in both directions; pass an empty
// marksBackupPath only when no fragment existed before the apply.
func (a *Applier) Restore(ctx context.Context, backupPath, marksBackupPath string) error {
	if a.Run == nil {
		return fmt.Errorf("multi_wan: runner is required")
	}
	data, err := a.Run.ReadFile(ctx, backupPath)
	if err != nil {
		return fmt.Errorf("read mwan3 backup: %w", err)
	}
	if err := a.Run.WriteFile(ctx, mwan3ConfigPath, data, 0600); err != nil {
		return fmt.Errorf("restore mwan3: %w", err)
	}
	if err := a.commitAndRestart(ctx); err != nil {
		return err
	}
	if marksBackupPath == "" {
		return nil
	}
	frag, err := a.Run.ReadFile(ctx, marksBackupPath)
	if err != nil {
		return fmt.Errorf("read marks backup: %w", err)
	}
	if err := a.Run.WriteFile(ctx, marksFragmentPath, frag, 0644); err != nil {
		return fmt.Errorf("restore marks fragment: %w", err)
	}
	if _, err := a.Run.Run(ctx, "fw4", "reload"); err != nil {
		return fmt.Errorf("reload firewall after restore: %w", err)
	}
	return nil
}
