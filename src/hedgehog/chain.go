package hedgehog

// CHAIN ACCESS — WHY AN INTERFACE, AND WHY NOT A BITCOIN NODE
//
// A router running TollGate has no Bitcoin node: the PR #944 research confirmed
// there is no bitcoind/bitcoin-cli on the device, no chain state, and very little
// spare flash. A Hedgehog channel nevertheless needs to know things the router
// cannot compute alone:
//
//   - does the funding output exist, and is it confirmed?
//   - is a spends-this-outpoint transaction in the mempool?
//   - how deep is it? (the CSV window is measured in BLOCKS)
//
// The operator's direction was explicit: assume the router is online and can
// reach a Bitcoin node over the internet, Electrum-style, and assume chain state
// is readily available without running a node on the router.
//
// So this file defines the MINIMUM surface the protocol needs and leaves the
// transport pluggable. That keeps three things possible at once:
//
//   1. the router uses an Electrum/SPV endpoint, with no local node;
//   2. tests use a direct bitcoind RPC on the bench machine;
//   3. the router-side verifier is testable with no chain at all (NullChainView).
//
// Only THREE calls are required. Anything that would need a full node -- script
// indexing, address history, raw block parsing -- is deliberately absent, because
// the router cannot do it and the protocol does not need it.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// TxStatus describes an outpoint's on-chain state.
type TxStatus struct {
	// Confirmed is true when the transaction is in a block.
	Confirmed bool
	// Height is the block height when Confirmed, else 0.
	Height int32
	// Mempool is true when the transaction is seen but unconfirmed.
	Mempool bool
}

// ChainView is the minimum chain access Hedgehog requires.
//
// Implementations MUST NOT be handed secrets: every call takes only public
// identifiers.
type ChainView interface {
	// GetTxStatus reports whether a transaction is confirmed or in the mempool.
	// A transaction unknown to the view is reported as neither (an empty
	// TxStatus), NOT as an error -- absence is normal while a spend is being
	// assembled.
	GetTxStatus(ctx context.Context, txid string) (TxStatus, error)

	// GetTxOutput returns the value and scriptPubKey of a specific output.
	// This is needed because a settlement transaction must know exactly which
	// output it is spending and how much it holds.
	GetTxOutput(ctx context.Context, txid string, vout uint32) (TxOut, error)

	// TipHeight returns the current chain tip height. The CSV window is measured
	// in blocks from the height at which the funding output was confirmed, so
	// progress through the window is tipHeight - confirmHeight.
	TipHeight(ctx context.Context) (int32, error)
}

// TxOut is a chain output as the view reports it.
type TxOut struct {
	Value        int64
	ScriptPubKey []byte
}

// Errors shared by implementations.
var (
	// ErrTxNotFound means the view does not know the transaction. Callers
	// should treat this as "not yet", not as a hard failure.
	ErrTxNotFound = errors.New("hedgehog: transaction not found")
	// ErrVoutNotFound means the transaction exists but has no such output.
	ErrVoutNotFound = errors.New("hedgehog: output index not found")
)

// ---------------------------------------------------------------------------
// A chain view with no chain: the router-side default and the test double
// ---------------------------------------------------------------------------

// NullChainView answers every query with "not found" and version 0.
//
// Its purpose is the router: a device with no chain access must still be able to
// VERIFY a spend locally (that is a pure script evaluation against known
// prevouts) without pretending it knows the chain. Returning "unknown" is
// honest; returning a guess would not be.
type NullChainView struct{}

// GetTxStatus always reports an unknown transaction.
func (NullChainView) GetTxStatus(context.Context, string) (TxStatus, error) {
	return TxStatus{}, nil
}

// GetTxOutput always reports a missing output.
func (NullChainView) GetTxOutput(context.Context, string, uint32) (TxOut, error) {
	return TxOut{}, ErrTxNotFound
}

// TipHeight always reports 0.
func (NullChainView) TipHeight(context.Context) (int32, error) { return 0, nil }

// ---------------------------------------------------------------------------
// A memory-backed view: used by the regtest lifecycle tests and by the router
// when an Electrum client has pushed observations in
// ---------------------------------------------------------------------------

// MemoryChainView is a ChainView backed by explicit observations. It is the
// shape an Electrum subscriber would populate: the client learns about a txid
// and records it here, then the protocol code asks this view rather than
// speaking Electrum directly.
type MemoryChainView struct {
	mu   sync.RWMutex
	txs  map[string]TxStatus
	outs map[string]TxOut
	tip  int32
}

// NewMemoryChainView returns an empty view.
func NewMemoryChainView() *MemoryChainView {
	return &MemoryChainView{txs: map[string]TxStatus{}, outs: map[string]TxOut{}}
}

func outKey(txid string, vout uint32) string { return fmt.Sprintf("%s:%d", txid, vout) }

// ObserveConfirmed records a confirmed transaction at a height and updates the
// tip if the transaction is at the tip.
func (m *MemoryChainView) ObserveConfirmed(txid string, height int32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.txs[txid] = TxStatus{Confirmed: true, Height: height}
	if height > m.tip {
		m.tip = height
	}
}

// ObserveMempool records an unconfirmed transaction.
func (m *MemoryChainView) ObserveMempool(txid string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.txs[txid] = TxStatus{Mempool: true}
}

// SetOutput records an output's value and script.
func (m *MemoryChainView) SetOutput(txid string, vout uint32, o TxOut) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outs[outKey(txid, vout)] = o
}

// SetTip sets the tip height, which advances the CSV window.
func (m *MemoryChainView) SetTip(height int32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tip = height
}

// GetTxStatus implements ChainView.
func (m *MemoryChainView) GetTxStatus(_ context.Context, txid string) (TxStatus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.txs[txid], nil
}

// GetTxOutput implements ChainView.
func (m *MemoryChainView) GetTxOutput(_ context.Context, txid string, vout uint32) (TxOut, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	o, ok := m.outs[outKey(txid, vout)]
	if !ok {
		return TxOut{}, ErrTxNotFound
	}
	return o, nil
}

// TipHeight implements ChainView.
func (m *MemoryChainView) TipHeight(context.Context) (int32, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tip, nil
}

// ---------------------------------------------------------------------------
// CSV watching — the relocated time obligation
// ---------------------------------------------------------------------------

// CSVWatch reports on progress through a channel's relative-lock window.
type CSVWatch struct {
	// CSVBlocks is the relative lock, in blocks.
	CSVBlocks uint16
	// ConfirmHeight is the height at which the funding output was confirmed.
	ConfirmHeight int32
	// TipHeight is the current tip.
	TipHeight int32
}

// Elapsed is how many blocks have passed since confirmation.
func (w CSVWatch) Elapsed() int32 {
	if w.TipHeight <= w.ConfirmHeight {
		return 0
	}
	return w.TipHeight - w.ConfirmHeight
}

// Expired reports whether the relative lock has elapsed, i.e. whether the
// unilateral-reclaim path is now spendable.
func (w CSVWatch) Expired() bool { return w.Elapsed() >= int32(w.CSVBlocks) }

// Remaining is how many blocks are left in the window. Zero once expired.
func (w CSVWatch) Remaining() int32 {
	r := int32(w.CSVBlocks) - w.Elapsed()
	if r < 0 {
		return 0
	}
	return r
}

// WatchWindow is the wall-clock duration of the window at 10-minute blocks.
func (w CSVWatch) WatchWindow() time.Duration {
	return time.Duration(w.CSVBlocks) * 10 * time.Minute
}

// String renders the watch for logs.
func (w CSVWatch) String() string {
	return fmt.Sprintf("csv=%d elapsed=%d remaining=%d expired=%t (~%s)",
		w.CSVBlocks, w.Elapsed(), w.Remaining(), w.Expired(), w.WatchWindow())
}

// CheckCSV builds a watch from the chain view.
//
// This is the operation the ROUTER performs, and it is the D5 time obligation in
// concrete form: somebody must call this and act on Remaining() before it hits
// zero. For a TollGate purchase that somebody is the router, which is online by
// definition -- which is why the obligation is affordable here even though the
// research recorded that Hedgehog "relocates" it rather than removing it.
func CheckCSV(ctx context.Context, view ChainView, fundingTxid string,
	csvBlocks uint16) (CSVWatch, error) {

	st, err := view.GetTxStatus(ctx, fundingTxid)
	if err != nil {
		return CSVWatch{}, fmt.Errorf("hedgehog: csv check: %w", err)
	}
	tip, err := view.TipHeight(ctx)
	if err != nil {
		return CSVWatch{}, fmt.Errorf("hedgehog: csv tip: %w", err)
	}
	w := CSVWatch{CSVBlocks: csvBlocks, TipHeight: tip}
	if st.Confirmed {
		w.ConfirmHeight = st.Height
	}
	return w, nil
}
