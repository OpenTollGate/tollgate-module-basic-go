//go:build testenv

package main

import (
	"os"
	"path/filepath"
)

func init() {
	os.Setenv("TOLLGATE_TEST_CONFIG_DIR", filepath.Join(os.TempDir(), "tollgate-main-test"))
	os.MkdirAll(os.Getenv("TOLLGATE_TEST_CONFIG_DIR"), 0755)
}
