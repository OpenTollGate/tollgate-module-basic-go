package merchant

import (
	"os"
	"path/filepath"
	"testing"
)

// #834: the journals' durability contract is only complete when the
// rename is durable, not just the temp file — syncParentDir exists to
// close that gap after every rename. These tests pin the helper's
// observable contract (opens, syncs, closes cleanly; preserves the file)
// and its error shape on a nonexistent directory.
func TestSyncParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "receive-intents.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := syncParentDir(path); err != nil {
		t.Fatalf("syncParentDir on a live directory: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file must survive the dir fsync: %v", err)
	}

	missing := filepath.Join(t.TempDir(), "gone", "f.json")
	if err := syncParentDir(missing); err == nil {
		t.Fatal("syncParentDir on a nonexistent directory must error")
	}
}
