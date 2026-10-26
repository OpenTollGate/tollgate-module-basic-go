package tollwallet

import (
	"errors"
	"strings"
	"syscall"
)

// IsStorageMmapUnsupported reports whether a wallet-creation error is the
// shared-mmap failure class: bbolt maps its database with
// mmap(PROT_READ|PROT_WRITE, MAP_SHARED), and filesystems that cannot back
// shared mappings — jffs2, i.e. the overlay of every squashfs NOR-flash
// OpenWrt target — reject that call with EINVAL, so the wallet can never
// initialize on them (#583; etcd-io/bbolt#258, boltdb/bolt#592).
//
// The errno sentinel is matched two ways. errors.Is covers the day the
// wrapping preserves the chain; the errno-text fallback covers today,
// because gonuts-tollgate v0.10.0 wraps bolt.Open with %v
// (wallet/storage/bolt.go: "error setting bolt db: %v"), which flattens
// syscall.EINVAL to its text and breaks the Unwrap chain. Scoped to errors
// from the wallet-creation path, the errno text is unambiguous — no other
// step in that chain surfaces a bare "invalid argument".
func IsStorageMmapUnsupported(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EINVAL) {
		return true
	}
	return strings.Contains(err.Error(), syscall.EINVAL.Error())
}
