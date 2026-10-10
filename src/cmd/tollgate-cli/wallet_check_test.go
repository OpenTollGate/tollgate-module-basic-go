package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// #505's corruption-tooling acceptance criterion: "wallet check unit on
// seeded corrupt DBs". A verdict of "intact" must require a structurally
// sound database; a corrupted one must be named as such, never pass.
// Writing these tests surfaced the reason the tool forks a worker: bbolt
// v1.4 PANICS on torn input — at open for a truncated file (recoverable
// in-process) and inside Tx.Check's own goroutine for meta-page
// corruption (fatal in-process). The meta-page case therefore runs the
// checker through the helper-process re-exec, exactly like the real
// worker, and asserts the crash maps to a corruption verdict. The
// leaf-value bit-rot case is deliberately absent — bbolt v1.4 has no data
// checksums, so value bit-rot is NOT detectable by tx.Check (documented
// limitation in the command help and the runbook, and an audit finding
// in #505).

// TestMain doubles as the crash-isolation helper: the meta-corruption test
// re-execs the test binary with TOLLGATE_CHECK_HELPER=worker and the path
// in the environment, TestMain runs the in-process checker instead of the
// suite, and the parent observes the exit code the real worker contract
// defines.
func TestMain(m *testing.M) {
	if os.Getenv("TOLLGATE_CHECK_HELPER") == "worker" {
		if err := checkWalletDBInProcess(os.Getenv("TOLLGATE_CHECK_HELPER_PATH")); err != nil {
			os.Exit(workerExitFindings)
		}
		os.Exit(workerExitIntact)
	}
	os.Exit(m.Run())
}

func seedHealthyWalletDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wallet.db")
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatalf("seed bolt db: %v", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte("mints"))
		if err != nil {
			return err
		}
		return b.Put([]byte("https://mint.test"), []byte(`{"counter":42}`))
	}); err != nil {
		db.Close()
		t.Fatalf("seed bucket: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seeded db: %v", err)
	}
	return path
}

func TestCheckWalletDB_HealthyDBIsIntact(t *testing.T) {
	path := seedHealthyWalletDB(t)
	if err := checkWalletDBInProcess(path); err != nil {
		t.Fatalf("healthy wallet.db must check clean, got: %v", err)
	}
}

func TestCheckWalletDB_MissingFileIsNotCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent-wallet.db")
	err := checkWalletDBInProcess(path)
	if err == nil {
		t.Fatal("absent wallet.db must not check clean")
	}
	if !errors.Is(err, ErrNoWalletDB) {
		t.Errorf("absent wallet.db must report the distinct no-database error, got: %v", err)
	}
}

// A truncated file (power cut during a grow) faults bbolt with SIGBUS
// (mmap page beyond EOF) — an unrecoverable runtime fault, not a panic,
// so this too must go through the worker contract.
func TestCheckWalletDB_TruncatedFileIsFlaggedViaWorker(t *testing.T) {
	path := seedHealthyWalletDB(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seeded db: %v", err)
	}
	if err := os.WriteFile(path, data[:len(data)/2], 0600); err != nil {
		t.Fatalf("write truncated db: %v", err)
	}

	verdict := runWorkerHelper(t, path)
	if verdict == nil || strings.Contains(verdict.Error(), ErrNoWalletDB.Error()) {
		t.Fatalf("truncated wallet.db must be flagged as corrupt, got: %v", verdict)
	}
}

// corruptMetaPage triggers the unrecoverable shape: the check goroutine's
// page assertions panic. The checker must still produce a corruption
// verdict — via the worker contract — which is the whole reason the tool
// forks.
func TestCheckWalletDB_CorruptMetaPageIsFlaggedViaWorker(t *testing.T) {
	path := seedHealthyWalletDB(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seeded db: %v", err)
	}
	for _, off := range []int{0, 512} {
		for i := 0; i < 64; i++ {
			data[off+i] ^= 0xFF
		}
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write corrupted db: %v", err)
	}

	verdict := runWorkerHelper(t, path)
	if verdict == nil {
		t.Fatal("corrupted wallet.db must not check clean")
	}
	if !strings.Contains(verdict.Error(), "corrupt") {
		t.Errorf("crash/findings verdict should name corruption, got: %v", verdict)
	}
}

// runWorkerHelper re-execs the test binary as the checker worker (the
// TestMain helper) and returns the mapped verdict for the exit it took.
func runWorkerHelper(t *testing.T, path string) error {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestMainHelperOnly", "-test.count=1")
	cmd.Env = append(os.Environ(),
		"TOLLGATE_CHECK_HELPER=worker",
		"TOLLGATE_CHECK_HELPER_PATH="+path,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected an exit status from the worker, got: %v (output: %s)", err, out)
	}
	return mapWorkerOutcome(exitErr.ExitCode(), string(out))
}

func TestMapWorkerOutcome(t *testing.T) {
	if err := mapWorkerOutcome(workerExitIntact, ""); err != nil {
		t.Errorf("intact exit must map to nil, got: %v", err)
	}
	if err := mapWorkerOutcome(workerExitFindings, "page 3: bad freelist"); err == nil ||
		!strings.Contains(err.Error(), "bad freelist") {
		t.Errorf("findings exit must surface the worker output, got: %v", err)
	}
	if err := mapWorkerOutcome(2, "panic: assertion failed"); err == nil ||
		!strings.Contains(err.Error(), "corrupt") {
		t.Errorf("crash exit must map to the corruption verdict, got: %v", err)
	}
}

func TestWalletDBPathHonorsTestConfigDir(t *testing.T) {
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", "/tmp/wallet-check-test")
	if got, want := walletDBPath(), "/tmp/wallet-check-test/wallet.db"; got != want {
		t.Errorf("walletDBPath() = %s, want %s", got, want)
	}
}
