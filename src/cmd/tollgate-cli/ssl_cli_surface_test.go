package main

import (
	"bytes"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The CLI's observable surface around the TLS identity: WHEN the service is told
// the identity changed, and WHAT a refusal prints. Both were wrong in opposite
// directions — the derived hop was committed after the reload that was supposed
// to deliver it, and the refusal that a shell branches on was printed twice.

// TestSSLApplyDerivesTheRedirectBeforeItReloadsUhttpd: the derived
// uhttpd.main.redirect_https must be committed BEFORE the identity is delivered
// to the services.
//
// uhttpd reads the option when it (re)starts, so a reload that runs first hands
// the running server the PREVIOUS value: `tollgate ssl apply` printed
// "redirect_https=1 (... covers this router)" while the live uhttpd kept serving
// :8080 over plain HTTP until something reloaded it again — the same defect class
// as the setup path this rule exists for (a value derived on paper but not the
// one being served). The removal paths already order it correctly
// (applyRedirectHTTPS, then reloadServices); the apply paths did not.
//
// The uhttpd init stub records what the RUNNING service would have read at the
// moment it was asked to reload, which is the only vantage that can see the
// difference.
func TestSSLApplyDerivesTheRedirectBeforeItReloadsUhttpd(t *testing.T) {
	root := redirectSSLPaths(t)
	stubUCI(t, routerUCI())
	t.Setenv("TMPDIR", t.TempDir())

	record := filepath.Join(root, "reload-saw-redirect_https")
	stub := "#!/bin/sh\n" +
		"case \"${1:-}\" in\n" +
		"  reload) uci -q get uhttpd.main.redirect_https > \"" + record + "\" 2>/dev/null || printf 'UNSET\\n' > \"" + record + "\" ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(uhttpdInitPath, []byte(stub), 0755); err != nil {
		t.Fatalf("write the uhttpd reload stub: %v", err)
	}
	// The attended path: the command delivers the identity to the services
	// itself, instead of leaving it to a caller that converges them.
	sslNoRestartFlag = false

	if _, err := captureStdout(t, func() error { return sslApply(nil) }); err != nil {
		t.Fatalf("ssl apply: %v", err)
	}

	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the uhttpd stub was never asked to reload: %v", err)
	}
	got := strings.TrimSpace(string(data))
	if got != "1" {
		t.Errorf("uhttpd was reloaded while uhttpd.main.redirect_https was %q, want \"1\": "+
			"the derived hop has to be committed before the service is told the identity changed, "+
			"or the operator is told about a redirect the running server does not serve", got)
	}
}

// TestSSLCoversPrintsItsRefusalOnce: the refusal is the answer, so it is said
// once.
//
// `ssl covers` is a question with a verdict — "covers: no — <reason>" on stdout
// plus a non-zero exit status, which is the contract the setup path branches on.
// On top of that it returns an error, and BOTH cobra and main() printed that
// error, so the same sentence landed on stderr twice and a caller grepping the
// log could not tell one refusal from two failures.
//
// Pinned at the seam where the duplication happened: the command's own error
// stream. main()'s single "Error: ..." line (the one every failing command
// prints) is not what this asserts — silence, not a second copy, is.
func TestSSLCoversPrintsItsRefusalOnce(t *testing.T) {
	redirectSSLPaths(t)
	stubUCI(t, routerUCI())
	placeholder := writeTestCertPEM(t, &x509.Certificate{
		Subject:  pkix.Name{CommonName: "OpenWrt"},
		DNSNames: []string{"OpenWrt"},
	})

	oldOut, oldErr := rootCmd.OutOrStdout(), rootCmd.ErrOrStderr()
	defer func() {
		rootCmd.SetOut(oldOut)
		rootCmd.SetErr(oldErr)
		rootCmd.SetArgs(nil)
	}()

	var cmdErr bytes.Buffer
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(&cmdErr)
	rootCmd.SetArgs([]string{"ssl", "covers", placeholder})

	if err := rootCmd.Execute(); err == nil {
		t.Fatal("a non-covering certificate must fail the command")
	}
	if strings.Contains(cmdErr.String(), "does not cover") {
		t.Errorf("the refusal was printed on the command's error stream as well (%q): main() already prints it, "+
			"so the operator sees the same failure twice", strings.TrimSpace(cmdErr.String()))
	}
}
