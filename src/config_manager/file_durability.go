package config_manager

import (
	"os"
	"path/filepath"
)

// writeFileDurably persists data at filePath with the crash-consistency
// discipline the #402 incident class demands: a plain os.WriteFile killed
// mid-write (power loss, procd respawn in the write window) leaves a
// truncated file, and every loader in this package routes a truncated file
// into backup-and-defaults — the operator's accepted mints, payout identity
// and even the Nostr signing key (identities.json carries
// owned_identities[0].privatekey) silently reverting to the factory set.
// Temp file + fsync + rename in the same directory means a reader always
// sees either the whole previous file or the whole new one; the fsync
// before the rename means the new content is on the medium, not just in
// the page cache, when the old file is replaced (#505 audit).
func writeFileDurably(filePath string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(filePath), ".cfg-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := renameIntoPlace(tmpName, filePath); err != nil {
		// A pinned inode (single-file bind mount — the cloud-lab lane mounts
		// configs at their /etc/tollgate paths; containerized deploys do the
		// same) cannot be renamed over. Fall back to the durable in-place
		// write rather than refusing to save; see writeInPlaceDurably.
		if inErr := writeInPlaceDurably(filePath, data); inErr != nil {
			return err
		}
		return nil
	}
	cleanup = false
	return nil
}

// renameIntoPlace is os.Rename, overridable by tests to model the
// environments where a rename onto the target path is impossible.
var renameIntoPlace = os.Rename

// writeInPlaceDurably is the fallback for environments a rename cannot
// serve: a single-file bind mount has its inode pinned, so rename(2)
// answers EBUSY no matter how the temp file is prepared. There,
// truncating and rewriting the mounted file is the best atomicity
// available — and strictly better than refusing to persist at all.
// A var (like renameIntoPlace) so tests can model a filesystem where
// every path fails and pin the old-file-survives contract.
var writeInPlaceDurably = func(filePath string, data []byte) error {
	f, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
