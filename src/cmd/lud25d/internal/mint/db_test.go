package mint

import (
	"testing"
)

// newTestDB creates a temporary in-memory SQLite database for testing.
func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
	})
	return db
}
