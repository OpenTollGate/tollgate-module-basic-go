package merchant

import (
	"log"
	"os"
	"path/filepath"
)

// fsyncDirAfterRename closes the durability gap of the journals' atomic
// write (#834): fsyncing the temp file does not make the RENAME durable —
// POSIX requires the containing directory to be fsynced as well, or a
// power cut can drop the latest write even though its bytes were synced.
// Failures are logged, never returned: by the time this runs the rename
// has already happened, so a save that persisted must not be reported as
// failed.
func fsyncDirAfterRename(filePath string) {
	dir := filepath.Dir(filePath)
	d, err := os.Open(dir)
	if err != nil {
		log.Printf("WARNING: could not open %s to fsync the journal directory: %v — a power cut may drop the latest %s write", dir, err, filepath.Base(filePath))
		return
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		log.Printf("WARNING: could not fsync the journal directory %s: %v — a power cut may drop the latest %s write", dir, err, filepath.Base(filePath))
	}
}
