package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

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

// ErrNoWalletDB reports that no wallet database exists at the checked path
// (fresh install or wrong --path) — a different situation than corruption.
var ErrNoWalletDB = fmt.Errorf("no wallet database found at the path")

// walletDBPath resolves the wallet database location the same way the
// daemon does: TOLLGATE_TEST_CONFIG_DIR redirects for the test harness; in
// production the wallet always lives beside the configs in /etc/tollgate.
func walletDBPath() string {
	if dir := os.Getenv("TOLLGATE_TEST_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "wallet.db")
	}
	return "/etc/tollgate/wallet.db"
}

// checkWalletDBInProcess opens the database read-only and runs bbolt's
// structural consistency check (page layout, freelist, key ordering, bucket
// references). It answers "intact" only when the file opens cleanly AND
// the B+tree walk finds nothing wrong. Two honest limitations, recorded in
// the runbook: bbolt v1.4 panics on some torn inputs instead of erroring
// (the worker subprocess converts that death into a corruption verdict),
// and there are no data-page checksums — a clean check proves structure,
// not byte-level fidelity against silent bit-rot.
func checkWalletDBInProcess(path string) (err error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrNoWalletDB, path)
		}
		return fmt.Errorf("stat wallet database: %w", err)
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("wallet database failed to open (corrupt or unreadable): bbolt panicked on this file: %v", r)
		}
	}()
	db, err := bolt.Open(path, 0600, &bolt.Options{ReadOnly: true})
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
	cmd := exec.Command(self, "wallet", "check-worker", "--path", path)
	out, err := cmd.CombinedOutput()
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
suspected corruption, or at any time read-only alongside the running daemon.

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
