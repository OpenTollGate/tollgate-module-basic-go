package merchant

// Pins the ONE property that makes keyset rotation survivable, in the library version
// this module pins: DLEQ verification must resolve the keyset from the PROOF itself
// (proof.Id), not from the mint's current active keyset.
//
// WHY THIS TEST EXISTS (issue #1057)
//
// gonuts-tollgate v0.13.0 ships VerifyProofsDLEQWithKeysets, which groups proofs by
// proof.Id and fetches the HISTORICAL keyset for each one — the correct behaviour after a
// mint rotates keysets. A mint that rotates (Minibits, observed 2026-07) then keeps
// honouring tokens minted under the old keyset.
//
// The failure this prevents is customer-visible and silent: a token minted before rotation
// is rejected with "invalid DLEQ proof" at spend time, and the holder's money is stranded.
// A UI-level workaround (removing Minibits from the mint list) hid that for months
// downstream; it was removed under the assumption the backend was fixed.
//
// The invariant is asserted against the PINNED library, so a dependency bump that drops or
// renames the keyed verifier — or a refactor that starts calling the legacy
// VerifyProofsDLEQ(proofs, activeKeyset) — fails here instead of in the field.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// dleqLibraryDir locates the pinned gonuts module source in the module cache.
func dleqLibraryDir(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/OpenTollGate/gonuts-tollgate").Output()
	if err != nil {
		t.Fatalf("go list -m gonuts-tollgate: %v", err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" || dir == "<nil>" {
		t.Fatal("gonuts-tollgate module directory not resolvable")
	}
	return dir
}

// TestPinnedGonutsVerifiesDLEQKeyedByProofID asserts the pinned library still provides a
// proof.Id-keyed DLEQ verifier, and that it resolves keysets through a fetcher rather than
// assuming the active keyset.
func TestPinnedGonutsVerifiesDLEQKeyedByProofID(t *testing.T) {
	dir := dleqLibraryDir(t)
	path := filepath.Join(dir, "cashu", "nuts", "nut12", "nut12.go")

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v (did the library move nut12?)", path, err)
	}

	var found bool
	var groupsByProofID bool
	var takesFetcher bool
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name == nil || fn.Name.Name != "VerifyProofsDLEQWithKeysets" {
			return true
		}
		found = true
		// A keysetFetcher parameter is what makes historical-keyset lookup possible.
		// keysetFetcher is a package-local named type, so it parses as *ast.Ident.
		if fn.Type.Params != nil {
			for _, p := range fn.Type.Params.List {
				switch tv := p.Type.(type) {
				case *ast.Ident:
					if tv.Name == "keysetFetcher" {
						takesFetcher = true
					}
				case *ast.SelectorExpr:
					if tv.Sel.Name == "keysetFetcher" {
						takesFetcher = true
					}
				}
			}
		}
		return true
	})

	if !found {
		t.Fatalf("pinned gonuts-tollgate no longer provides VerifyProofsDLEQWithKeysets: "+
			"after a keyset rotation every pre-rotation token would fail with "+
			"'invalid DLEQ proof' (issue #1057). Do not merge a dependency bump that drops it.", )
	}

	// The signature is unexported-typed (keysetFetcher is a private alias in nut12), so
	// assert it by source text, which is stable and reviewable.
	src, err := exec.Command("sed", "-n", "40,110p", path).Output()
	if err != nil {
		t.Fatalf("read nut12 body: %v", err)
	}
	body := string(src)
	if strings.Contains(body, "proofsByKeyset[proof.Id]") {
		groupsByProofID = true
	}
	if !groupsByProofID {
		t.Errorf("VerifyProofsDLEQWithKeysets no longer groups proofs by proof.Id — "+
			"the keyset-rotation fix has regressed (issue #1057). body:\n%s", body)
	}
	if !takesFetcher {
		t.Errorf("VerifyProofsDLEQWithKeysets no longer accepts a keysetFetcher, so it "+
			"cannot fetch a historical keyset (issue #1057)")
	}
}

// TestNoLegacyActiveKeysetDLEQCallInModule asserts this module does not bypass the keyed
// verifier. If/when the module gains its own verification call site, it must use the
// proof.Id-keyed path; a call to the legacy VerifyProofsDLEQ(proofs, activeKeyset) is the
// exact bug this issue is about.
func TestNoLegacyActiveKeysetDLEQCallInModule(t *testing.T) {
	out, err := exec.Command("grep", "-rn", "--include=*.go", "VerifyProofsDLEQ(", ".").Output()
	if err != nil {
		// grep exits 1 when there are no matches — that is the expected, safe state today.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return
		}
		t.Fatalf("grep: %v", err)
	}
	// Filter out the keyed variant and this test file.
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" || strings.Contains(line, "WithKeysets") || strings.Contains(line, "dleq_keyed_test.go") {
			continue
		}
		t.Errorf("legacy active-keyset DLEQ verification call site found (issue #1057): %s", line)
	}
}

// TestPinnedGonutsWalletWiresTheKeyedDLEQVerify closes the real gap this issue was about.
//
// The keyed verifier existing is not enough: the wallet must actually CALL it on the
// receive/swap path. In gonuts v0.13.0 it does (wallet.go, Receive and Swap). If a future
// bump reverts those call sites to the legacy active-keyset form, historical tokens stop
// verifying after any mint keyset rotation — the Minibits failure — with no compile error
// and no unit-test signal anywhere upstream.
func TestPinnedGonutsWalletWiresTheKeyedDLEQVerify(t *testing.T) {
	dir := dleqLibraryDir(t)
	walletSrc := filepath.Join(dir, "wallet", "wallet.go")

	out, err := exec.Command("grep", "-c", "nut12.VerifyProofsDLEQWithKeysets(", walletSrc).Output()
	if err != nil {
		t.Fatalf("grep %s: %v", walletSrc, err)
	}
	n := strings.TrimSpace(string(out))
	if n == "0" || n == "" {
		t.Fatalf("gonuts wallet no longer calls VerifyProofsDLEQWithKeysets: after a keyset "+
			"rotation every pre-rotation token fails with 'invalid DLEQ proof' (issue #1057). "+
			"Grep result: %q", n)
	}

	// The legacy call must NOT appear on the wallet path.
	legacy, _ := exec.Command("grep", "-n", "nut12.VerifyProofsDLEQ(", walletSrc).Output()
	for _, line := range strings.Split(string(legacy), "\n") {
		if line == "" || strings.Contains(line, "WithKeysets") {
			continue
		}
		t.Errorf("legacy active-keyset DLEQ call reintroduced in the wallet (issue #1057): %s", line)
	}
	t.Logf("gonuts wallet wires the keyed DLEQ verify at %s call site(s)", n)
}
