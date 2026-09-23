//go:build testenv && !cdk_wallet

package merchant

import (
	"bytes"
	"log"

	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

// Log hygiene for the token paths that leave the module.
//
// Two log calls printed the first 50 characters of a serialized Cashu token —
// enough to identify the note, and printed on the paths that create one
// (`CreatePaymentToken`) and that receive one (`Fund`). The length and the mint
// URL are the useful part for an operator; the preview is the part that must not
// be written to a log that is copied into a chat when something goes wrong.

// capturedStdLogs redirects the standard library logger into a buffer, the way
// the merchant's production code writes.
func capturedStdLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	buf := &bytes.Buffer{}
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})

	return buf
}

// serializableToken is a token whose serialized form is long enough that a
// 50-character preview would be a real leak.
type serializableToken struct {
	tollwallet.Token
	serialized string
}

func (t serializableToken) Mint() string               { return "https://log-hygiene.example.com" }
func (t serializableToken) Amount() uint64             { return 1 }
func (t serializableToken) Serialize() (string, error) { return t.serialized, nil }
func (t serializableToken) Close()                     {}

// tokenIssuingWallet reports a balance so CreatePaymentToken proceeds, and
// returns a token whose serialized form is the value under test.
type tokenIssuingWallet struct {
	tollwallet.WalletPort
	token tollwallet.Token
}

func (w *tokenIssuingWallet) GetBalanceByMint(string) uint64 { return 1000 }
func (w *tokenIssuingWallet) GetBalance() uint64             { return 1000 }
func (w *tokenIssuingWallet) Send(amount uint64, mintURL string, includeFees bool) (tollwallet.Token, error) {
	return w.token, nil
}

func TestCreatePaymentTokenDoesNotLogTheToken(t *testing.T) {
	serialized := "cashuB" + strings.Repeat("test-note-body-", 8)
	m := &Merchant{tollwallet: &tokenIssuingWallet{token: serializableToken{serialized: serialized}}}

	logs := capturedStdLogs(t)

	token, err := m.CreatePaymentToken("https://log-hygiene.example.com", 1)
	if err != nil {
		t.Fatalf("CreatePaymentToken: %v", err)
	}
	if token != serialized {
		t.Fatalf("CreatePaymentToken returned %q, want the wallet's token", token)
	}

	out := logs.String()
	if strings.Contains(out, serialized) {
		t.Fatalf("the spendable token is in the log output:\n%s", out)
	}
	if strings.Contains(out, serialized[:24]) {
		t.Fatalf("a prefix of the spendable token is in the log output:\n%s", out)
	}
	if !strings.Contains(out, "length=") {
		t.Fatalf("the log does not record the token length, which is the useful part:\n%s", out)
	}
}

func TestFundDoesNotLogTheToken(t *testing.T) {
	// A syntactically long, well-formed-looking token body. Fund decodes it
	// before doing anything else, so the failing decode path is fine — the log
	// lines that matter are written before and around it.
	token := "cashuB" + strings.Repeat("operator-funded-note-", 4)

	m := &Merchant{}
	logs := capturedStdLogs(t)

	if _, err := m.Fund(token); err == nil {
		t.Fatal("Fund on a zero-value merchant should fail to receive the token")
	}

	out := logs.String()
	if strings.Contains(out, token) {
		t.Fatalf("the spendable token is in the log output:\n%s", out)
	}
	if strings.Contains(out, token[:24]) {
		t.Fatalf("a prefix of the spendable token is in the log output:\n%s", out)
	}
	if !strings.Contains(out, "length") {
		t.Fatalf("the log does not record the token length, which is the useful part:\n%s", out)
	}
}
