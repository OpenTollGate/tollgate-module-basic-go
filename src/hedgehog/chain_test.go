package hedgehog

import (
	"context"
	"testing"
	"time"
)

// TestNullChainViewReportsUnknownNotGuessed is the router-side contract: a device
// with no chain access must answer "unknown", never a guess. Returning a plausible
// guess would let unwatched state look confirmed.
func TestNullChainViewReportsUnknownNotGuessed(t *testing.T) {
	ctx := context.Background()
	var v ChainView = NullChainView{}

	st, err := v.GetTxStatus(ctx, "00")
	if err != nil {
		t.Fatalf("absence must not be an error: %v", err)
	}
	if st.Confirmed || st.Mempool {
		t.Errorf("null view must report neither confirmed nor mempool, got %+v", st)
	}
	if _, err := v.GetTxOutput(ctx, "00", 0); err == nil {
		t.Error("null view must report a missing output as an error")
	}
	tip, err := v.TipHeight(ctx)
	if err != nil || tip != 0 {
		t.Errorf("null view tip must be 0 with no error, got %d, %v", tip, err)
	}
}

// TestMemoryChainViewObserves covers the double the lifecycle tests use.
func TestMemoryChainViewObserves(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryChainView()

	if st, _ := m.GetTxStatus(ctx, "abc"); st.Confirmed || st.Mempool {
		t.Error("unknown tx must report neither state")
	}
	m.ObserveMempool("abc")
	if st, _ := m.GetTxStatus(ctx, "abc"); !st.Mempool || st.Confirmed {
		t.Errorf("expected mempool-only, got %+v", st)
	}
	m.ObserveConfirmed("abc", 100)
	if st, _ := m.GetTxStatus(ctx, "abc"); !st.Confirmed || st.Height != 100 {
		t.Errorf("expected confirmed at 100, got %+v", st)
	}
	if tip, _ := m.TipHeight(ctx); tip != 100 {
		t.Errorf("tip should follow the confirmed height, got %d", tip)
	}

	m.SetOutput("abc", 1, TxOut{Value: 500, ScriptPubKey: []byte{0x51}})
	o, err := m.GetTxOutput(ctx, "abc", 1)
	if err != nil {
		t.Fatal(err)
	}
	if o.Value != 500 || len(o.ScriptPubKey) != 1 {
		t.Errorf("output round-trip wrong: %+v", o)
	}
	if _, err := m.GetTxOutput(ctx, "abc", 2); err == nil {
		t.Error("missing vout must error")
	}
}

// TestCSVWindowProgress is the D5 time obligation measured in blocks. This is the
// arithmetic the WATCHER must get right: if Remaining() is miscomputed the
// watchtower fires too late and the counterparty can steal.
func TestCSVWindowProgress(t *testing.T) {
	cases := []struct {
		name                 string
		confirm, tip         int32
		csv                  uint16
		wantElapsed          int32
		wantExpired          bool
		wantRemaining        int32
	}{
		{"just confirmed", 100, 100, 10, 0, false, 10},
		{"midway", 100, 105, 10, 5, false, 5},
		{"exactly at lock", 100, 110, 10, 10, true, 0},
		{"past lock", 100, 130, 10, 30, true, 0},
		// Tip behind confirmation must clamp to zero, never go negative: a
		// negative elapsed would make an unexpired lock look expired.
		{"tip behind confirm (reorg)", 100, 90, 10, 0, false, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := CSVWatch{CSVBlocks: tc.csv, ConfirmHeight: tc.confirm, TipHeight: tc.tip}
			if got := w.Elapsed(); got != tc.wantElapsed {
				t.Errorf("Elapsed: got %d want %d", got, tc.wantElapsed)
			}
			if got := w.Expired(); got != tc.wantExpired {
				t.Errorf("Expired: got %t want %t", got, tc.wantExpired)
			}
			if got := w.Remaining(); got != tc.wantRemaining {
				t.Errorf("Remaining: got %d want %d", got, tc.wantRemaining)
			}
			if got := w.WatchWindow(); got != time.Duration(tc.csv)*10*time.Minute {
				t.Errorf("WatchWindow mismatch: %s", got)
			}
		})
	}
}

// TestCheckCSVFromView wires the watch to a chain view.
func TestCheckCSVFromView(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryChainView()
	m.ObserveConfirmed("fund", 500)
	m.SetTip(507)

	w, err := CheckCSV(ctx, m, "fund", 2016)
	if err != nil {
		t.Fatal(err)
	}
	if w.ConfirmHeight != 500 {
		t.Errorf("confirm height should be 500, got %d", w.ConfirmHeight)
	}
	if w.Elapsed() != 7 {
		t.Errorf("elapsed should be 7, got %d", w.Elapsed())
	}
	if w.Expired() {
		t.Error("2016-block window must not be expired after 7 blocks")
	}
	if w.Remaining() != 2009 {
		t.Errorf("remaining should be 2009, got %d", w.Remaining())
	}
	t.Logf("the router's watch: %s", w)

	// An unknown funding tx must report as unconfirmed rather than erroring.
	w2, err := CheckCSV(ctx, m, "unknown-tx", 2016)
	if err != nil {
		t.Fatalf("unknown funding tx must not error: %v", err)
	}
	if w2.ConfirmHeight != 0 || w2.Expired() {
		t.Errorf("unknown funding tx must show an un-started window: %+v", w2)
	}
}
