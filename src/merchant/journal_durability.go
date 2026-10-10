package merchant

import (
	"fmt"
	"os"
	"path/filepath"
)

// syncParentDir fsyncs the directory containing path after an atomic
// rename into it. The three money journals (receive intents, owed grants,
// quote store) fsync the temp file before renaming — but on ext4-class
// filesystems the rename itself is only durable after the directory entry
// is synced: without this, a power cut can drop the latest journal write
// whose contents were already fsynced. For these stores the newest record
// IS the crash-recovery evidence (#502 persist-before-effect; #834) — the
// #834 audit class — so the durability contract is only complete when the
// rename is durable too. A failure here surfaces as a save error: the
// record must be treated as not-yet-durable, exactly like a failed
// temp-file Sync would be.
func syncParentDir(path string) error {
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open directory for fsync: %w", err)
	}
	if err := d.Sync(); err != nil {
		d.Close()
		return fmt.Errorf("fsync directory: %w", err)
	}
	return d.Close()
}
