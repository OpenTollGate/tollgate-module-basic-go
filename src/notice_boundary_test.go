package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/nbd-wtf/go-nostr"
)

// noticeRecorder captures what the HTTP boundary asks the merchant to announce.
type noticeRecorder struct {
	mu    sync.Mutex
	got   []string // customerPubkey arguments, in call order
	codes []string
}

func (n *noticeRecorder) record(code, customerPubkey string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.got = append(n.got, customerPubkey)
	n.codes = append(n.codes, code)
}

func (n *noticeRecorder) recipients() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]string, len(n.got))
	copy(out, n.got)
	return out
}

// boundaryMerchant records every notice request and returns a notice-shaped
// event, so the boundary's response path runs too.
type boundaryMerchant struct {
	rec *noticeRecorder
}

func (m *boundaryMerchant) CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error) {
	m.rec.record(code, customerPubkey)
	return &nostr.Event{Kind: 21023, Content: message}, nil
}
func (m *boundaryMerchant) PurchaseSession(string, string) (*nostr.Event, error) {
	return &nostr.Event{Kind: 21023, Content: "refused"}, nil
}

// --- the rest of MerchantInterface, unused by these probes ---

func (m *boundaryMerchant) CreatePaymentToken(string, uint64) (string, error) { return "", nil }
func (m *boundaryMerchant) CreatePaymentTokenWithOverpayment(string, uint64, uint64, uint64) (string, error) {
	return "", nil
}
func (m *boundaryMerchant) DrainMint(string) (string, uint64, error) { return "", 0, nil }
func (m *boundaryMerchant) RequestLightningInvoice(string, string, uint64) (*merchant.LightningInvoice, error) {
	return nil, nil
}
func (m *boundaryMerchant) GetLightningInvoiceStatus(string, string) (*merchant.LightningQuoteStatus, error) {
	return nil, nil
}
func (m *boundaryMerchant) GetAcceptedMints() []config_manager.MintConfig { return nil }
func (m *boundaryMerchant) Fund(string) (uint64, error)                   { return 0, nil }
func (m *boundaryMerchant) RebindSession(string, string) (*merchant.CustomerSession, error) {
	return nil, nil
}
func (m *boundaryMerchant) GetSession(string) (*merchant.CustomerSession, error) { return nil, nil }
func (m *boundaryMerchant) GetSessionState(string) (merchant.SessionState, error) {
	return merchant.SessionStateNone, nil
}
func (m *boundaryMerchant) AddAllotment(string, string, uint64) (*merchant.CustomerSession, error) {
	return nil, nil
}
func (m *boundaryMerchant) GetUsage(string) (string, error)       { return "", nil }
func (m *boundaryMerchant) GetBalance() uint64                    { return 0 }
func (m *boundaryMerchant) GetBalanceByMint(string) uint64        { return 0 }
func (m *boundaryMerchant) GetAllMintBalances() map[string]uint64 { return nil }
func (m *boundaryMerchant) GetAdvertisement() string              { return "" }
func (m *boundaryMerchant) StartPayoutRoutine()                   {}
func (m *boundaryMerchant) StartDataUsageMonitoring()             {}
func (m *boundaryMerchant) IssueSessionTicket(string) (string, int64, error) {
	return "", 0, nil
}
func (m *boundaryMerchant) SetOnReachableSetChanged(func()) {}

// #950's invariant is that a device identifier never occupies the `p` tag of a
// kind-21023 notice. #968 enforces it at the choke point (createNoticeEvent
// drops any recipient that is not a 64-hex Nostr pubkey) and stopped passing
// macAddress from the merchant's own error paths.
//
// But it MISSED the HTTP boundary in main.go: HandleRootPost still handed
// macAddress to sendNoticeResponse as the customerPubkey on three paths, and
// main.go is not in #968's diff at all. Those notices are returned to the
// caller and published by the relay path, so the MAC reaches the wire.
//
// The choke point happens to neutralise them, which is exactly why a test that
// only asserts the final tag shape cannot catch this — it stays green while the
// call site reads as if the MAC is passed deliberately. So these tests check
// the call sites themselves, not just what survives downstream.

// sendNoticeResponse is the single point every notice in this file goes
// through, so the recipient argument at each call site is the thing to pin.
// A source scan is used rather than driving the handler because the handler
// needs a resolvable client identity before it reaches these paths, and a
// test that stubs identity resolution would be asserting its own stub.

// macAddress is the only device-identifier variable in scope at these call
// sites. Passing it as the notice recipient is the #950 bug.
func TestHandleRootPostCallSitesNeverPassTheMACAsRecipient(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	var checked int
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != "sendNoticeResponse" {
			return true
		}
		// sendNoticeResponse(w, m, status, level, code, message, customerPubkey)
		if len(call.Args) < 7 {
			t.Errorf("sendNoticeResponse at %s has %d args, want 7 — signature changed, update this test",
				fset.Position(call.Pos()), len(call.Args))
			return true
		}
		checked++
		last := call.Args[6]
		if id, ok := last.(*ast.Ident); ok && id.Name == "macAddress" {
			t.Errorf("%s: sendNoticeResponse passes macAddress as the notice recipient — device identifiers must never occupy p (#950)",
				fset.Position(call.Pos()))
			return true
		}
		// Anything other than an explicit empty string is worth a look.
		if lit, ok := last.(*ast.BasicLit); !ok || lit.Kind != token.STRING {
			t.Errorf("%s: notice recipient is %T, not a literal — expected \"\" (no Nostr identity is known at this boundary)",
				fset.Position(call.Pos()), last)
			return true
		}
		if lit := last.(*ast.BasicLit); lit.Value != `""` {
			t.Errorf("%s: notice recipient is %s, want \"\" — only a real Nostr pubkey may be cited",
				fset.Position(call.Pos()), lit.Value)
		}
		return true
	})

	if checked == 0 {
		t.Fatal("found no sendNoticeResponse call sites — the scan is looking at the wrong file")
	}
}

// The boundary function itself must forward the recipient it is given, and must
// not invent one from the request. This drives the real function.
func TestSendNoticeResponseForwardsAnEmptyRecipient(t *testing.T) {
	rec := &noticeRecorder{}
	w := httptest.NewRecorder()

	sendNoticeResponse(w, &boundaryMerchant{rec: rec}, http.StatusBadRequest,
		"error", "probe", "message", "")

	got := rec.recipients()
	if len(got) != 1 {
		t.Fatalf("CreateNoticeEvent called %d time(s), want 1", len(got))
	}
	if got[0] != "" {
		t.Fatalf("recipient forwarded as %q, want empty", got[0])
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// A real pubkey passed at the boundary must still be forwarded — otherwise the
// invariant above could be "satisfied" by dropping every recipient.
func TestSendNoticeResponseForwardsARealPubkey(t *testing.T) {
	rec := &noticeRecorder{}
	w := httptest.NewRecorder()

	const pubkey = "70208b3e272936bb31cd7849beb6b673e18b75ee0cdbd0822f77386f5db0156a"
	sendNoticeResponse(w, &boundaryMerchant{rec: rec}, http.StatusOK,
		"info", "probe", "message", pubkey)

	got := rec.recipients()
	if len(got) != 1 {
		t.Fatalf("CreateNoticeEvent called %d time(s), want 1", len(got))
	}
	if got[0] != pubkey {
		t.Fatalf("recipient forwarded as %q, want the pubkey %q", got[0], pubkey)
	}
}

// Guard against the scan silently going stale: the file it parses must be the
// one that defines HandleRootPost.
func TestMainGoDefinesHandleRootPost(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fn.Name.Name == "HandleRootPost" {
			return
		}
	}
	t.Fatal("main.go does not define HandleRootPost — the scan is pointed at the wrong file")
}
