package cli

import (
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
)

// #824: `status` must distinguish "merchant present" from "merchant
// degraded", and carry the classified reason (storage-mmap vs no reachable
// mints) so the board shows the operator the real story. The wiring is an
// optional-interface assertion on the merchant — these tests pin the
// surface: fields populated when the merchant answers WalletDegradedInfo,
// zero-valued when it does not (full merchant) or when no provider exists.

// degradedStatusStub embeds the interface (satisfying it without
// implementing its methods — none are called on this path) and adds the
// assertion surface the status command looks for.
type degradedStatusStub struct {
	merchant.MerchantInterface
	degraded bool
	reason   string
}

func (s degradedStatusStub) WalletDegradedInfo() (bool, string) {
	return s.degraded, s.reason
}

func TestHandleStatusCommand_SurfacesDegradedWalletInfo(t *testing.T) {
	s := &CLIServer{
		startTime:        time.Now(),
		merchantProvider: merchant.NewMutexMerchantProvider(degradedStatusStub{degraded: true, reason: "wallet storage does not support shared mmap (jffs2 overlay?)"}),
	}

	resp := s.processCommand(CLIMessage{Command: "status"})
	status, ok := resp.Data.(ServiceStatus)
	if !ok {
		t.Fatalf("expected ServiceStatus, got %T", resp.Data)
	}
	if !status.WalletOK {
		t.Error("WalletOK should still hold for a present-but-degraded merchant (unchanged semantics)")
	}
	if !status.WalletDegraded {
		t.Error("WalletDegraded must be true when the merchant reports the degraded state")
	}
	if status.WalletReason != "wallet storage does not support shared mmap (jffs2 overlay?)" {
		t.Errorf("WalletReason should carry the classified cause verbatim, got: %q", status.WalletReason)
	}
}

func TestHandleStatusCommand_FullMerchantKeepsDefaults(t *testing.T) {
	// No WalletDegradedInfo on this merchant: the assertion fails and the
	// defaults hold — a full merchant is not degraded.
	s := &CLIServer{
		startTime:        time.Now(),
		merchantProvider: merchant.NewMutexMerchantProvider(fullMerchantStub{}),
	}

	resp := s.processCommand(CLIMessage{Command: "status"})
	status, ok := resp.Data.(ServiceStatus)
	if !ok {
		t.Fatalf("expected ServiceStatus, got %T", resp.Data)
	}
	if status.WalletDegraded || status.WalletReason != "" {
		t.Errorf("full merchant must keep zero-valued degraded fields, got (%v, %q)", status.WalletDegraded, status.WalletReason)
	}
}

func TestHandleStatusCommand_NilProviderSafe(t *testing.T) {
	s := &CLIServer{startTime: time.Now()}

	resp := s.processCommand(CLIMessage{Command: "status"})
	status, ok := resp.Data.(ServiceStatus)
	if !ok {
		t.Fatalf("expected ServiceStatus, got %T", resp.Data)
	}
	if status.WalletDegraded || status.WalletReason != "" {
		t.Errorf("nil provider must keep zero-valued degraded fields, got (%v, %q)", status.WalletDegraded, status.WalletReason)
	}
}

type fullMerchantStub struct {
	merchant.MerchantInterface
}
