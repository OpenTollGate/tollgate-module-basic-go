//go:build testenv

package main

import "testing"

// The daemon takes no arguments except --version, which OpenWrt package CI
// uses to verify built binaries report their version.
func TestVersionRequested_whenArgsInspected(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"long flag", []string{"tollgate-wrt", "--version"}, true},
		{"single-dash flag", []string{"tollgate-wrt", "-version"}, true},
		{"no args", []string{"tollgate-wrt"}, false},
		{"unrelated flag", []string{"tollgate-wrt", "--foo"}, false},
		{"version in non-flag position", []string{"tollgate-wrt", "start", "--version"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := versionRequested(tc.args); got != tc.want {
				t.Fatalf("versionRequested(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}
