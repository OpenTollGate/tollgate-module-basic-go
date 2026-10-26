package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	bolt "go.etcd.io/bbolt"
)

// The offline wallet consistency check from #505's corruption-tooling
// scope: fund-relevant state on flash can tear, and "is wallet.db intact?"
// must be answerable without the daemon (a corrupt DB is exactly the state
// where the daemon may not come up). Read-only; the recovery flow lives in
// docs/wallet-recovery-runbook.md.
//
// ARCHITECTURE: the check runs in a forked worker process, because bbolt
// v1.4 does not reliably return corruption as an error — its page and
// freelist assertions PANIC on torn input, sometimes on the open path
// (recoverable) and sometimes inside Tx.Check's own goroutine (not
// recoverable in-process; both shapes were observed while writing the
// seeded-corruption tests). A corruption checker must never die on
// corruption, so the worker's death is simply another corruption verdict.
// One wrinkle the seeded-corruption tests also caught: that goroutine's
// deferred channel close fires mid-panic, so a worker whose main goroutine
// wins the race to exit reports a corrupt database as intact — the meta
// pages, where the race lives, are therefore validated up front by
// precheckMetaPages and never reach the racing path at all.

// ErrNoWalletDB reports that no wallet database exists at the checked path
// (fresh install or wrong --path) — a different situation than corruption.
var ErrNoWalletDB = fmt.Errorf("no wallet database found at the path")

// ErrWalletDBLocked reports that the database is held by the running daemon:
// bbolt's read-only open takes LOCK_SH and, with no Timeout, retries the
// flock forever. A verdict is better than an indefinite silent hang.
var ErrWalletDBLocked = fmt.Errorf("wallet database is locked by the running daemon — stop the service or check a copy of the file")

// walletDBPath resolves the wallet database location the same way the
// daemon does: TOLLGATE_TEST_CONFIG_DIR redirects for the test harness; in
// production the wallet always lives beside the configs in /etc/tollgate.
func walletDBPath() string {
	if dir := os.Getenv("TOLLGATE_TEST_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "wallet.db")
	}
	return "/etc/tollgate/wallet.db"
}

// bbolt's on-disk format constants (from go.etcd.io/bbolt's internal/common:
// page.go and types.go — stable wire-format values the module does not export).
const (
	bboltPageHeaderSize = 16 // id (8) + flags (2) + count (2) + overflow (4)
	bboltMetaPageFlag   = 0x04
	bboltMetaMagic      = 0xED0CDAED
	bboltMetaVersion    = 2
)

// precheckMetaPages validates the structural identity of the two meta pages
// straight from the file, before bbolt's own machinery runs. This closes a
// race the seeded-corruption tests surfaced: Tx.Check walks pages on its own
// goroutine and PANICS on a torn meta page (the page id/flags assertions),
// but its deferred close(ch) fires while the panic is still unwinding — so a
// consumer that drains the findings channel and exits before the runtime's
// fatal exit observes zero findings for a corrupt database. In the worker
// that is a scheduling race between os.Exit(0) and the panic's exit(2), and
// it was lost roughly one run in ten on CI-grade hardware: `wallet check`
// could report a torn wallet.db "structurally consistent". Header identity
// of pages 0 and 1 is exactly the invariant whose violation panics, checked
// here with plain pread calls that cannot race anything.
//
// One honest limit remains, recorded with the others in the runbook: a torn
// header deeper inside the B+tree (a leaf or branch page id) still panics
// the walk goroutine and still resolves through the worker's crash-versus-
// exit race. Walking the tree ourselves to check every page would mean
// reimplementing bbolt's checker; the meta pages — the first sectors flash
// writes touch, and the shape #505 worried about — are covered
// deterministically here.
func precheckMetaPages(path string, size int64) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open for meta-page precheck: %w", err)
	}
	defer f.Close()

	readHeader := func(off int64, wantID uint64) error {
		var hdr [bboltPageHeaderSize]byte
		if _, err := f.ReadAt(hdr[:], off); err != nil {
			return fmt.Errorf("meta page %d unreadable (file is %d bytes): %w", wantID, size, err)
		}
		id := binary.LittleEndian.Uint64(hdr[0:8])
		flags := binary.LittleEndian.Uint16(hdr[8:10])
		if id != wantID || flags != bboltMetaPageFlag {
			return fmt.Errorf("meta page %d header is torn: page identifies as id=%d flags=%#x, want id=%d flags=%#x", wantID, id, flags, wantID, bboltMetaPageFlag)
		}
		return nil
	}
	if err := readHeader(0, 0); err != nil {
		return err
	}

	// Locate page 1 via the page size meta page 0 declares, but trust that
	// number only when magic, version and the size itself are self-
	// consistent; otherwise leave page 1 to bbolt's own candidate scan (a
	// torn page 0 is already flagged above, and Open fails outright when no
	// meta validates at all).
	var meta [16 + 12]byte // page header + magic, version, pageSize
	if _, err := f.ReadAt(meta[:], 0); err != nil {
		return fmt.Errorf("meta page 0 unreadable (file is %d bytes): %w", size, err)
	}
	if binary.LittleEndian.Uint32(meta[16:20]) != bboltMetaMagic ||
		binary.LittleEndian.Uint32(meta[20:24]) != bboltMetaVersion {
		return nil
	}
	ps := binary.LittleEndian.Uint32(meta[24:28])
	if ps < 512 || ps > 1<<20 || ps&(ps-1) != 0 {
		return nil
	}
	if size < int64(ps)*2 {
		return fmt.Errorf("file is %d bytes, smaller than the two %d-byte meta pages every bbolt database has", size, ps)
	}
	return readHeader(int64(ps), 1)
}

// checkWalletDBInProcess opens the database read-only and runs bbolt's
// structural consistency check (page layout, freelist, key ordering, bucket
// references). It answers "intact" only when the file opens cleanly AND
// the B+tree walk finds nothing wrong. Three honest limitations, recorded in
// the runbook: bbolt v1.4 panics on some torn inputs instead of erroring,
// and those panics race the worker's exit — so the meta pages (the shape
// that races) are validated deterministically by precheckMetaPages before
// bbolt runs, while deeper shapes still lean on the worker subprocess
// converting its death into a corruption verdict; there are no data-page
// checksums — a clean check proves structure, not byte-level fidelity
// against silent bit-rot; and a torn older meta page beside a valid newer
// one is a healthy double-buffered database, not corruption.
func checkWalletDBInProcess(path string) (err error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrNoWalletDB, path)
		}
		return fmt.Errorf("stat wallet database: %w", err)
	}
	if err := precheckMetaPages(path, info.Size()); err != nil {
		return fmt.Errorf("wallet database failed to open (corrupt or unreadable): %w", err)
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("wallet database failed to open (corrupt or unreadable): bbolt panicked on this file: %v", r)
		}
	}()
	db, err := bolt.Open(path, 0600, &bolt.Options{ReadOnly: true, Timeout: 2 * time.Second})
	if errors.Is(err, bolt.ErrTimeout) {
		return fmt.Errorf("%w", ErrWalletDBLocked)
	}
	if err != nil {
		return fmt.Errorf("wallet database failed to open (corrupt or unreadable): %w", err)
	}
	defer db.Close()
	if err := db.View(func(tx *bolt.Tx) error {
		// bbolt v1.4 reports findings on a channel; drain it fully so the
		// verdict names every problem, not just the first.
		var findings []error
		for e := range tx.Check() {
			findings = append(findings, e)
		}
		return errors.Join(findings...)
	}); err != nil {
		return fmt.Errorf("wallet database consistency check found problems: %w", err)
	}
	return nil
}

// Worker exit codes: the contract between the hidden worker command and
// the public verdict mapping.
const (
	workerExitIntact   = 0
	workerExitFindings = 1
	workerExitLocked   = 3
)

// runWalletCheck executes the worker subprocess against path and maps its
// outcome. An intact database exits 0; findings exit 1 with the report on
// stderr; anything else — including a worker killed by a bbolt assertion
// panic — is a corruption verdict, because a checker that survives only
// healthy databases proves nothing about corrupt ones.
func runWalletCheck(path string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate the tollgate-cli binary to fork the check worker: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, "wallet", "check-worker", "--path", path)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("wallet check worker did not return within 60s (a wedged worker is not a verdict) — check a copy of the database")
	}
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return mapWorkerOutcome(exitErr.ExitCode(), string(out))
	}
	return fmt.Errorf("run the wallet check worker: %w (output: %s)", err, out)
}

// mapWorkerOutcome turns a worker exit code plus captured output into the
// operator-facing verdict.
func mapWorkerOutcome(exitCode int, output string) error {
	switch exitCode {
	case workerExitIntact:
		return nil
	case workerExitFindings:
		return fmt.Errorf("wallet database consistency check found problems:\n%s", output)
	case workerExitLocked:
		return ErrWalletDBLocked
	default:
		return fmt.Errorf("wallet database is corrupt or unreadable (the checker did not survive it — treat any partial output as findings):\n%s", output)
	}
}

var walletCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Verify wallet.db integrity (offline bbolt consistency check)",
	Long: `Verify the structural integrity of the wallet database.

Runs bbolt's consistency check (page layout, freelist, key ordering, bucket
references) against wallet.db in read-only mode, in an isolated worker
process: bbolt panics on some torn inputs, and the checker must survive the
databases it exists to judge. Run it from the recovery runbook
(docs/wallet-recovery-runbook.md): with the service stopped after a
suspected corruption, or against a stopped service. Against a live daemon it reports "locked by the running daemon" (the wallet holds the database lock); it does not wait indefinitely.

A clean check proves structure, not byte-level fidelity: bbolt v1.4 has no
data-page checksums, so bit-rot inside leaf values is not detectable here.
Exit code 0 = intact, 1 = problems found or file missing.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, _ := cmd.Flags().GetString("path")
		if path == "" {
			path = walletDBPath()
		}
		if err := runWalletCheck(path); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "FAIL %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "ok   %s: structurally consistent\n", path)
		return nil
	},
}

// walletCheckWorkerCmd is the hidden half of wallet check: it runs the
// in-process checker and speaks the exit-code contract above. Hidden
// because it is an implementation detail of crash isolation, not an
// operator surface.
var walletCheckWorkerCmd = &cobra.Command{
	Use:    "check-worker",
	Short:  "internal: in-process wallet.db check worker (crash isolation for `wallet check`)",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, _ := cmd.Flags().GetString("path")
		if path == "" {
			path = walletDBPath()
		}
		if err := checkWalletDBInProcess(path); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "%v\n", err)
			if errors.Is(err, ErrWalletDBLocked) {
				os.Exit(workerExitLocked)
			}
			os.Exit(workerExitFindings)
		}
		return nil
	},
}

func init() {
	walletCheckCmd.Flags().String("path", "", "path to the wallet database (default: /etc/tollgate/wallet.db, $TOLLGATE_TEST_CONFIG_DIR/wallet.db when set)")
	walletCheckWorkerCmd.Flags().String("path", "", "path to the wallet database")
	walletCmd.AddCommand(walletCheckCmd)
	walletCmd.AddCommand(walletCheckWorkerCmd)
}
