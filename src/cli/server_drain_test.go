package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/nbd-wtf/go-nostr"
)

// scriptedDrainMerchant is a deterministic fake for handleCashuDrain. It
// models the merchant contract relevant to drains:
//
//   - GetAllMintBalances reports a static "listed" view (aliases of the
//     same logical mint both report the shared underlying balance, which
//     is exactly what the gonuts wallet does when its per-mint registry
//     contains two URL aliases for one physical mint).
//   - DrainMint draws down the shared underlying balance of the mint's
//     group. The first alias of a group succeeds and produces a token;
//     any later alias of the same group fails with the same
//     "no balance available" error the real wallet produces.
//   - failWith forces an unconditional per-mint failure (e.g. network
//     error on a genuinely independent mint).
type scriptedDrainMerchant struct {
	// groupOf maps mint URL -> logical mint group ("a", "b", ...).
	groupOf map[string]string
	// underlying holds the shared balance per group.
	underlying map[string]uint64
	// failWith forces DrainMint errors for specific mints.
	failWith map[string]error
	// calls records DrainMint invocation order.
	calls []string
	// beforeDrain, when set, runs at the top of every DrainMint call.
	beforeDrain func(mintURL string)
}

func (m *scriptedDrainMerchant) DrainMint(mintURL string) (string, uint64, error) {
	if m.beforeDrain != nil {
		m.beforeDrain(mintURL)
	}
	m.calls = append(m.calls, mintURL)
	if err, ok := m.failWith[mintURL]; ok {
		return "", 0, err
	}
	group := m.groupOf[mintURL]
	balance := m.underlying[group]
	if balance == 0 {
		return "", 0, fmt.Errorf("no balance available for mint %s", mintURL)
	}
	// A successful drain is irreversible: the underlying balance is gone.
	m.underlying[group] = 0
	return fmt.Sprintf("token-%s", group), balance, nil
}

func (m *scriptedDrainMerchant) GetAllMintBalances() map[string]uint64 {
	out := make(map[string]uint64)
	for alias, group := range m.groupOf {
		out[alias] = m.underlying[group]
	}
	return out
}

// Remaining MerchantInterface methods are unused by handleCashuDrain.
func (m *scriptedDrainMerchant) CreatePaymentToken(mintURL string, amount uint64) (string, error) {
	return "", nil
}
func (m *scriptedDrainMerchant) CreatePaymentTokenWithOverpayment(mintURL string, amount uint64, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error) {
	return "", nil
}
func (m *scriptedDrainMerchant) GetAcceptedMints() []config_manager.MintConfig { return nil }
func (m *scriptedDrainMerchant) GetBalance() uint64                            { return 0 }
func (m *scriptedDrainMerchant) GetBalanceByMint(mintURL string) uint64 {
	return m.underlying[m.groupOf[mintURL]]
}
func (m *scriptedDrainMerchant) PurchaseSession(cashuToken string, macAddress string) (*nostr.Event, error) {
	return nil, nil
}
func (m *scriptedDrainMerchant) GetAdvertisement() string  { return "" }
func (m *scriptedDrainMerchant) StartPayoutRoutine()       {}
func (m *scriptedDrainMerchant) StartDataUsageMonitoring() {}
func (m *scriptedDrainMerchant) CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error) {
	return nil, nil
}
func (m *scriptedDrainMerchant) GetSession(macAddress string) (*merchant.CustomerSession, error) {
	return nil, nil
}
func (m *scriptedDrainMerchant) AddAllotment(macAddress, metric string, amount uint64) (*merchant.CustomerSession, error) {
	return nil, nil
}
func (m *scriptedDrainMerchant) GetUsage(macAddress string) (string, error) { return "", nil }
func (m *scriptedDrainMerchant) Fund(cashuToken string) (uint64, error)     { return 0, nil }
func (m *scriptedDrainMerchant) RequestLightningInvoice(macAddress, mintURL string, amount uint64) (*merchant.LightningInvoice, error) {
	return nil, nil
}
func (m *scriptedDrainMerchant) GetLightningInvoiceStatus(quoteID, macAddress string) (*merchant.LightningQuoteStatus, error) {
	return nil, nil
}
func (m *scriptedDrainMerchant) SetOnReachableSetChanged(func()) {}

func newDrainTestServer(t *testing.T, m merchant.MerchantInterface) *CLIServer {
	// Drain successes are journaled; keep the journal in a temp dir so
	// tests never touch /etc/tollgate.
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", t.TempDir())
	return NewCLIServer(nil, merchant.NewMutexMerchantProvider(m), nil, nil, nil)
}

// drainResponseJSON marshals the CLIResponse the way a socket client
// would receive it, so assertions work against the wire shape rather
// than internal Go types.
func drainResponseJSON(t *testing.T, resp CLIResponse) map[string]interface{} {
	t.Helper()
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return parsed
}

// responseTokens extracts data.tokens as a slice of raw maps.
func responseTokens(t *testing.T, parsed map[string]interface{}) []map[string]interface{} {
	t.Helper()
	data, ok := parsed["data"].(map[string]interface{})
	if !ok {
		return nil
	}
	rawTokens, ok := data["tokens"].([]interface{})
	if !ok {
		return nil
	}
	tokens := make([]map[string]interface{}, 0, len(rawTokens))
	for _, rt := range rawTokens {
		if tm, ok := rt.(map[string]interface{}); ok {
			tokens = append(tokens, tm)
		}
	}
	return tokens
}

func responseErrors(t *testing.T, parsed map[string]interface{}) []map[string]interface{} {
	t.Helper()
	data, ok := parsed["data"].(map[string]interface{})
	if !ok {
		return nil
	}
	rawErrors, ok := data["errors"].([]interface{})
	if !ok {
		return nil
	}
	errs := make([]map[string]interface{}, 0, len(rawErrors))
	for _, re := range rawErrors {
		if em, ok := re.(map[string]interface{}); ok {
			errs = append(errs, em)
		}
	}
	return errs
}

// tokenForMint reports whether the response carries a token string
// produced for the given logical group.
func containsToken(tokens []map[string]interface{}, token string) bool {
	for _, tok := range tokens {
		if s, ok := tok["token"].(string); ok && s == token {
			return true
		}
	}
	return false
}

// TestHandleCashuDrain_PartialFailure_RetainsSuccessfulToken is the core
// fund-safety regression for issue #375:
//
//	A successful irreversible drain of mint A must never be lost because a
//	later drain of mint B fails.
//
// Mint A (50 sats) and mint B (30 sats) are independent mints; B's drain
// fails with a network-style error after A's drain already succeeded.
// Map iteration order decides which mint is hit first, so whichever mint
// drains first plays the role of A — the invariant must hold either way.
func TestHandleCashuDrain_PartialFailure_RetainsSuccessfulToken(t *testing.T) {
	const (
		mintA = "https://mint-a.test/Bitcoin"
		mintB = "https://mint-b.test/Bitcoin"
	)
	m := &scriptedDrainMerchant{
		groupOf:    map[string]string{mintA: "a", mintB: "b"},
		underlying: map[string]uint64{"a": 50, "b": 30},
		failWith:   map[string]error{mintB: fmt.Errorf("connection refused")},
	}
	s := newDrainTestServer(t, m)

	resp := s.handleCashuDrain(nil)
	parsed := drainResponseJSON(t, resp)

	// The token that was irreversibly produced MUST be in the response.
	tokens := responseTokens(t, parsed)
	if !containsToken(tokens, "token-a") {
		t.Fatalf("response discarded the token from the successfully drained mint: tokens=%v (full=%v)", tokens, parsed)
	}

	// The response must not pretend the operation was atomic.
	if resp.Success {
		t.Fatalf("expected success=false for partial drain, got true: %v", parsed)
	}

	// The failed mint must be reported per-mint.
	errs := responseErrors(t, parsed)
	if len(errs) != 1 {
		t.Fatalf("expected exactly 1 per-mint error, got %v (full=%v)", errs, parsed)
	}
	if errs[0]["mint_url"] != mintB {
		t.Fatalf("per-mint error should reference %s, got %v", mintB, errs[0])
	}
}

// TestHandleCashuDrain_Issue375_DuplicateAliases_SharedBalance reproduces
// the exact trigger from issue #375: the wallet registry lists two URL
// aliases (trailing slash difference) for the same logical mint, both
// showing the same 50-sat balance. Whichever alias the map iteration
// visits first drains successfully; the second observes zero balance and
// fails. The successful token must remain recoverable and the command
// must report partial failure accurately.
func TestHandleCashuDrain_Issue375_DuplicateAliases_SharedBalance(t *testing.T) {
	const (
		alias1 = "https://mint.example/Bitcoin"
		alias2 = "https://mint.example/Bitcoin/"
	)
	m := &scriptedDrainMerchant{
		groupOf:    map[string]string{alias1: "shared", alias2: "shared"},
		underlying: map[string]uint64{"shared": 50},
	}
	s := newDrainTestServer(t, m)

	resp := s.handleCashuDrain(nil)
	parsed := drainResponseJSON(t, resp)

	tokens := responseTokens(t, parsed)
	if !containsToken(tokens, "token-shared") {
		t.Fatalf("response discarded the token from the successfully drained alias: tokens=%v (full=%v)", tokens, parsed)
	}
	if resp.Success {
		t.Fatalf("expected success=false when one alias drain failed, got true: %v", parsed)
	}
	errs := responseErrors(t, parsed)
	if len(errs) != 1 {
		t.Fatalf("expected exactly 1 per-mint error for the zero-balance alias, got %v (full=%v)", errs, parsed)
	}
}

// TestHandleCashuDrain_JournalMakesTokenDurableBeforeNextMint pins the
// crash-safety boundary: a successfully drained mint's token must be in the
// journal BEFORE the next mint's irreversible drain starts. Map iteration
// order decides which mint is first, so the assertion is expressed as
// "when the second drain starts, the first drain's token is already on
// disk" — the guarantee must hold in both orders.
func TestHandleCashuDrain_JournalMakesTokenDurableBeforeNextMint(t *testing.T) {
	const (
		mintA = "https://mint-a.test/Bitcoin"
		mintB = "https://mint-b.test/Bitcoin"
	)
	m := &scriptedDrainMerchant{
		groupOf:    map[string]string{mintA: "a", mintB: "b"},
		underlying: map[string]uint64{"a": 50, "b": 30},
	}
	s := newDrainTestServer(t, m)

	journalPath := drainJournalPath()
	m.beforeDrain = func(mintURL string) {
		if len(m.calls) == 0 {
			// First drain: nothing journaled yet is fine.
			return
		}
		firstGroup := m.groupOf[m.calls[0]]
		journalBytes, err := os.ReadFile(journalPath)
		if err != nil {
			t.Fatalf("second drain started but journal unreadable: %v", err)
		}
		if !strings.Contains(string(journalBytes), "token-"+firstGroup) {
			t.Fatalf("second drain (%s) started before first drain's token %q was journaled; journal: %s", mintURL, "token-"+firstGroup, journalBytes)
		}
	}

	resp := s.handleCashuDrain(nil)
	if !resp.Success {
		t.Fatalf("expected full success, got: %v", resp)
	}
	if len(m.calls) != 2 {
		t.Fatalf("expected both mints drained, calls: %v", m.calls)
	}
}

// TestHandleCashuDrain_JournalRecordsEveryToken verifies journal content
// after a fully successful drain.
func TestHandleCashuDrain_JournalRecordsEveryToken(t *testing.T) {
	const (
		mintA = "https://mint-a.test/Bitcoin"
		mintB = "https://mint-b.test/Bitcoin"
	)
	m := &scriptedDrainMerchant{
		groupOf:    map[string]string{mintA: "a", mintB: "b"},
		underlying: map[string]uint64{"a": 50, "b": 30},
	}
	s := newDrainTestServer(t, m)

	if resp := s.handleCashuDrain(nil); !resp.Success {
		t.Fatalf("expected success, got: %v", resp)
	}

	journalBytes, err := os.ReadFile(drainJournalPath())
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	var entries []drainJournalEntry
	for _, line := range strings.Split(strings.TrimSpace(string(journalBytes)), "\n") {
		if line == "" {
			continue
		}
		var entry drainJournalEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("bad journal line %q: %v", line, err)
		}
		entries = append(entries, entry)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 journal entries, got %d: %v", len(entries), entries)
	}
	journaled := map[string]drainJournalEntry{}
	for _, entry := range entries {
		journaled[entry.MintURL] = entry
	}
	if e := journaled[mintA]; e.Token != "token-a" || e.AmountSats != 50 {
		t.Errorf("journal entry for %s: %+v", mintA, e)
	}
	if e := journaled[mintB]; e.Token != "token-b" || e.AmountSats != 30 {
		t.Errorf("journal entry for %s: %+v", mintB, e)
	}
}

// TestHandleCashuDrain_JournalFailure_StopsFurtherDrains verifies the
// stronger durability invariant: when a drained token cannot be made
// durable, the coordinator must not start the next mint's irreversible
// drain. The drained token is still reported in the response.
func TestHandleCashuDrain_JournalFailure_StopsFurtherDrains(t *testing.T) {
	const (
		mintA = "https://mint-a.test/Bitcoin"
		mintB = "https://mint-b.test/Bitcoin"
	)
	dir := t.TempDir()
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", dir)
	// Occupy the journal path with a directory so the append fails.
	if err := os.MkdirAll(filepath.Join(dir, "wallet-drain-journal.jsonl"), 0o700); err != nil {
		t.Fatalf("block journal path: %v", err)
	}

	m := &scriptedDrainMerchant{
		groupOf:    map[string]string{mintA: "a", mintB: "b"},
		underlying: map[string]uint64{"a": 50, "b": 30},
	}
	s := newDrainTestServer(t, m)
	// newDrainTestServer sets its own env var; re-point it at the blocked dir.
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", dir)

	resp := s.handleCashuDrain(nil)
	parsed := drainResponseJSON(t, resp)

	if len(m.calls) != 1 {
		t.Fatalf("journal failure must stop further drains; DrainMint calls: %v", m.calls)
	}
	tokens := responseTokens(t, parsed)
	if len(tokens) != 1 {
		t.Fatalf("the drained token must still be reported, tokens: %v (full=%v)", tokens, parsed)
	}
	if resp.Success {
		t.Fatalf("expected success=false when durability failed, got: %v", parsed)
	}
	errs := responseErrors(t, parsed)
	if len(errs) != 1 || !strings.Contains(fmt.Sprint(errs[0]["error"]), "durable") {
		t.Fatalf("expected one durability error entry, got: %v (full=%v)", errs, parsed)
	}
}

// TestHandleCashuDrain_AllMintsSucceed is the control: two independent
// mints both drain; full success must keep both tokens and total.
func TestHandleCashuDrain_AllMintsSucceed(t *testing.T) {
	const (
		mintA = "https://mint-a.test/Bitcoin"
		mintB = "https://mint-b.test/Bitcoin"
	)
	m := &scriptedDrainMerchant{
		groupOf:    map[string]string{mintA: "a", mintB: "b"},
		underlying: map[string]uint64{"a": 50, "b": 30},
	}
	s := newDrainTestServer(t, m)

	resp := s.handleCashuDrain(nil)
	parsed := drainResponseJSON(t, resp)

	if !resp.Success {
		t.Fatalf("expected success for full drain, got: %v", parsed)
	}
	tokens := responseTokens(t, parsed)
	if !containsToken(tokens, "token-a") || !containsToken(tokens, "token-b") {
		t.Fatalf("expected both tokens in response, got: %v", tokens)
	}
	data := parsed["data"].(map[string]interface{})
	if data["total_sats"] != float64(80) {
		t.Fatalf("expected total 80, got %v", data["total_sats"])
	}
}
