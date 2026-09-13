package main

import (
	"bytes"
	"strings"
	"testing"
)

// The OpenWrt packages feed CI runs built binaries with --version and
// requires the embedded version string in the output. Cobra only provides
// the --version flag when rootCmd.Version is set.
func TestRootCmdVersionFlag_whenInvoked_printsEmbeddedVersion(t *testing.T) {
	// Given
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	defer func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
	}()

	// When
	rootCmd.SetArgs([]string{"--version"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute(--version) failed: %v", err)
	}

	// Then
	got := out.String()
	if !strings.Contains(got, "tollgate version "+version) {
		t.Fatalf("--version output %q does not contain embedded version %q", got, version)
	}
}
