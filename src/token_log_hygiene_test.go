package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

// productionGoFiles lists the module's non-test Go files under the working
// directory (the test runs from src/), including the nested modules — the log
// calls that matter live in merchant/, cli/ and valve/ as well as in main.go.
func productionGoFiles(t *testing.T) []string {
	t.Helper()

	var files []string
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base == "vendor" || base == ".git" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walking the source tree: %v", err)
	}
	return files
}

func readFileString(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// Log hygiene on the money path.
//
// The body of `POST /` IS the bearer instrument: `extractCashuToken` returns the
// request body verbatim unless it is a kind-21000 event, and whoever reads the
// log line can spend it. The handler logged the whole body at debug
// ("Received POST request" with field `body`) — and debug is the first level an
// operator turns on when a payment fails, which is exactly when a log is copied
// into a chat or a bug report. Removing the value is the fix; gating on the log
// level is not.
//
// What the log must carry instead is a correlate: a length, and a salted
// fingerprint that is stable for the same token (so a payment can be followed
// through the log and matched against the customer's report) and meaningless to
// anyone else (16 hex chars of HMAC-SHA256 under a per-install salt).

// capturedLogs redirects the standard logrus logger into a buffer for the
// duration of the test and returns the buffer.
//
// The formatter is forced to the uncoloured text form: the coloured one wraps
// each key and value in ANSI escapes, so a field renders as `key=value` on a
// terminal while the captured bytes are `key` + ESC + `=value` — the assertions
// below would then pass or fail depending on whether the test ran under a TTY.
func capturedLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	buf := &bytes.Buffer{}
	prevOut := logrus.StandardLogger().Out
	prevLevel := logrus.GetLevel()
	prevFormatter := logrus.StandardLogger().Formatter
	logrus.SetOutput(buf)
	logrus.SetLevel(logrus.DebugLevel)
	logrus.SetFormatter(&logrus.TextFormatter{DisableColors: true})
	t.Cleanup(func() {
		logrus.SetOutput(prevOut)
		logrus.SetLevel(prevLevel)
		logrus.SetFormatter(prevFormatter)
	})

	return buf
}

// ansiEscape matches the colour sequences the production formatter emits:
// InitializeGlobalLogger configures logrus with `ForceColors: true`, so a test
// that asserts on captured log text must not depend on the formatter it happens
// to catch — under ForceColors every key and value is wrapped, and `body_len=`
// is then `body_len` + ESC…m + `=` in the bytes. The assertions below strip the
// escapes, so they mean the same thing in either formatter.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiEscape.ReplaceAllString(s, "") }

// spendableTokenBody is a distinctive token-shaped body. It carries no real
// proofs (nothing here is spendable) but it is the shape the money path accepts
// and the shape a leak would expose.
func spendableTokenBody() string {
	return "cashuB" + strings.Repeat("test-note-body-", 8)
}

func postToken(t *testing.T, token string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(token))
	req.RemoteAddr = testClientIP + ":4321"
	w := httptest.NewRecorder()
	HandleRootPost(w, req)
	return w
}

// The central assertion: whatever the handler logs, the spendable value is not
// in it — and the correlate that replaces it is.
func TestPostRootDoesNotLogTheSpendableToken(t *testing.T) {
	fake := &identityMerchant{}
	useIdentityMerchant(fake)
	resolvedClient(t, testClientIP, testClientMAC)

	logs := capturedLogs(t)
	token := spendableTokenBody()

	w := postToken(t, token)
	if w.Code != http.StatusOK {
		t.Fatalf("POST / returned %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	out := logs.String()
	plain := stripANSI(out)
	if strings.Contains(plain, token) {
		t.Fatalf("the spendable token is in the log output:\n%s", plain)
	}
	// A partial leak is still a leak: the body was logged whole, and any prefix
	// long enough to identify the token is enough to correlate it.
	if strings.Contains(plain, token[:24]) {
		t.Fatalf("a prefix of the spendable token is in the log output:\n%s", plain)
	}
	if !strings.Contains(plain, "body_len=") {
		t.Fatalf("the log does not record the body length:\n%s", plain)
	}
	if !regexp.MustCompile(`body_sha=[0-9a-f]{16}`).MatchString(plain) {
		t.Fatalf("the log does not record a 16-hex-char body fingerprint:\n%s", plain)
	}
}

// The fingerprint exists to be *correlated*: the same token must produce the
// same value in every log line that mentions it, and two different tokens must
// not collide (a journal that keys on it depends on both properties).
func TestTokenFingerprintIsStableAndDistinctInTheLog(t *testing.T) {
	fake := &identityMerchant{}
	useIdentityMerchant(fake)
	resolvedClient(t, testClientIP, testClientMAC)

	logs := capturedLogs(t)
	token := spendableTokenBody()

	postToken(t, token)
	postToken(t, token)
	postToken(t, "cashuB"+strings.Repeat("different-note-body-", 4))

	matches := regexp.MustCompile(`body_sha=([0-9a-f]{16})`).FindAllStringSubmatch(stripANSI(logs.String()), -1)
	if len(matches) != 3 {
		t.Fatalf("expected one fingerprint per request, got %d in:\n%s", len(matches), stripANSI(logs.String()))
	}
	if matches[0][1] != matches[1][1] {
		t.Fatalf("the same token produced two fingerprints (%s, %s); the correlate is not stable",
			matches[0][1], matches[1][1])
	}
	if matches[0][1] == matches[2][1] {
		t.Fatalf("two different tokens produced the same fingerprint (%s)", matches[0][1])
	}
}

// Identifiers whose value is (or contains) the serialized token.
var tokenCarryingIdentifiers = map[string]bool{
	"body":         true,
	"bodyStr":      true,
	"cashuToken":   true,
	"tokenStr":     true,
	"tokenString":  true,
	"tokenPreview": true,
	"paymentToken": true,
	"rawToken":     true,
	"token":        true,
}

// Identifiers whose value carries a proof's secret. A Cashu proof's `Secret` IS
// its spending condition (NUT-10): for an anyone-can-spend note it is the
// mint-accepted random string, for a P2PK/HTLC note the preimage or the
// signature that satisfies the lock — so a log line holding one hands the reader
// the note exactly as a logged token does. `proof:`/`proofs:` dumps are the
// shape a debugging edit reaches for first, which is why the set holds the names
// rather than only the field access. The list is deliberately short — the names
// this codebase uses, and adding a new one is a one-line change — while
// proofSecretAccessor below covers every `x.Secret` whatever the receiver is
// called.
//
// `secret` is a name this guard looks *for*, not a credential, and it trips
// detect-secrets' "Secret Keyword" heuristic on the `"name": true` shape of a
// map literal: hence the hook's own inline allowlist pragma rather than a
// baseline entry (the local CLI is 1.5.0 and does not flag the line, the pinned
// hook is 1.4.0 and does, so a baseline entry could not be generated to match).
var proofCarryingIdentifiers = map[string]bool{
	"proof":  true,
	"proofs": true,
	"secret": true, // pragma: allowlist secret
}

// proofSecretAccessor matches a secret (or a proof set) read off any receiver:
// `proof.Secret`, `p.Secret`, `wallet.Proofs()`. The bare-argument matcher below
// cannot see these — the value is not a bare identifier — and this is the shape
// the codebase's own resolver uses (`nut10.DeserializeSecret(proof.Secret)`).
var proofSecretAccessor = regexp.MustCompile(`\.\s*(?:Secret|Proofs)\b`)

// A logging call, whatever the logger. `fmt.Errorf`/`errors.New` are not
// logging: they match the `.Errorf(`/`.New(` shape but write nothing.
var logCallShape = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\.(?:Debug|Info|Warn|Error|Trace|Debugf|Infof|Warnf|Errorf|Print|Printf|Println)\(`)

func isLoggingCall(line string) bool {
	if strings.Contains(line, "log.Print") || strings.Contains(line, "mainLogger.") {
		return true
	}
	for _, m := range logCallShape.FindAllStringSubmatch(line, -1) {
		if m[1] != "fmt" && m[1] != "errors" {
			return true
		}
	}
	return false
}

// A bare identifier in argument position: after a comma, terminated by a
// comma or the closing parenthesis. Receiver/method forms (`x.Mint()`) and
// wrapped forms (`len(cashuToken)`) do not match — a length and a mint URL are
// exactly what may be logged. Accessor forms are covered by the caller's
// accessor regex instead.
var bareLogArgument = regexp.MustCompile(`,\s*([A-Za-z_][A-Za-z0-9_]*)\s*[,)]`)

// spendableFinding is one logging call that carries spendable material.
type spendableFinding struct {
	path string
	line int
	text string
	what string
}

// scanLogCalls walks the module's production (non-test) Go files — the nested
// modules under src/ included, because the log calls that matter live in
// merchant/, cli/, tollwallet/ and valve/ as well as in main.go — and reports
// every logging call that passes one of `carried` as a bare argument, or reads
// an identifier matching `accessor` (nil to skip). Findings are reported per
// line, with every flagged value on it, so one bad edit produces one message.
//
// The guard catches the shapes this codebase writes: a value passed straight to
// a logger, and a `.Secret`/`.Proofs` read. A dump of a proof held in an opaque
// variable (`log.Printf("p=%v", p)`) is NOT caught, and no name list can catch
// it — that stays a review responsibility.
func scanLogCalls(t *testing.T, carried map[string]bool, accessor *regexp.Regexp) []spendableFinding {
	t.Helper()

	files := productionGoFiles(t)
	if len(files) < 10 {
		t.Fatalf("expected to scan the module's production files, found %d — the guard is not looking where it thinks", len(files))
	}

	var findings []spendableFinding
	for _, path := range files {
		for i, line := range strings.Split(readFileString(t, path), "\n") {
			if !isLoggingCall(line) {
				continue
			}

			var hits []string
			seen := map[string]bool{}
			add := func(what string) {
				if !seen[what] {
					seen[what] = true
					hits = append(hits, what)
				}
			}

			for _, m := range bareLogArgument.FindAllStringSubmatch(line, -1) {
				if carried[m[1]] {
					add(m[1])
				}
			}
			if accessor != nil {
				for _, m := range accessor.FindAllString(line, -1) {
					add(strings.TrimSpace(m))
				}
			}

			if len(hits) > 0 {
				findings = append(findings, spendableFinding{
					path: path,
					line: i + 1,
					text: strings.TrimSpace(line),
					what: strings.Join(hits, ", "),
				})
			}
		}
	}
	return findings
}

// TestNoLogCallCanReceiveTheToken is a source guard rather than a behaviour test.
// The capture tests above prove today's paths are clean; this one catches the
// next edit that puts the instrument back into a log line, which is the shape the
// original defect had (`mainLogger.WithField("body", bodyStr).Debug(...)`) and
// which no runtime test can catch before it ships.
//
// A logging call is flagged when it passes a bare, token-carrying identifier as
// an argument — a whole token or a prefix of one. `len(cashuToken)` and
// `paymentCashuToken.Mint()` are not flagged: a length and a mint URL are exactly
// what may be logged. A false positive is escaped by naming the argument
// differently or by logging a derived value, not by weakening this test.
func TestNoLogCallCanReceiveTheToken(t *testing.T) {
	for _, f := range scanLogCalls(t, tokenCarryingIdentifiers, nil) {
		t.Errorf("%s:%d: a logging call receives the token-carrying value %q:\n	%s",
			f.path, f.line, f.what, f.text)
	}
}

// TestNoLogCallCanReceiveAProofSecret is the other half of the same guard: the
// token is one spendable representation of a note and a proof secret is another,
// and the audit behind this file asked for both to be asserted, not just the
// token ("verify no level, debug included, prints the token or a proof secret").
// A log line holding `proofs=` or `proof.Secret` is worth exactly as much to a
// reader of the log as the serialized token, and today no production path writes
// one — the assertion exists so the next debug line does not become the first.
func TestNoLogCallCanReceiveAProofSecret(t *testing.T) {
	for _, f := range scanLogCalls(t, proofCarryingIdentifiers, proofSecretAccessor) {
		t.Errorf("%s:%d: a logging call receives a proof-carried value %q:\n	%s",
			f.path, f.line, f.what, f.text)
	}
}
