package merchant

import (
	"sync"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

// recordingWallet captures AcceptMint calls. Every other WalletPort
// method is inherited from the embedded nil interface and panics if
// touched — this suite only exercises runtime admission.
type recordingWallet struct {
	tollwallet.WalletPort
	mu       sync.Mutex
	accepted []string
}

func (r *recordingWallet) AcceptMint(mintURL string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.accepted = append(r.accepted, mintURL)
	return nil
}

// A configured mint that was unreachable while the wallet was built must
// be admitted once the health tracker sees it reachable (#481): without
// AdmitReachableMints the wallet's accepted set stays frozen at the boot
// probe and the mint's tokens are rejected forever.
func TestAdmitReachableMintsAdmitsLateMint(t *testing.T) {
	config := mintConfigWithURLs("http://mint-a.test", "http://mint-b.test")
	tracker := newTestTracker(config, nil)

	// Boot state: mint-a answered the probe, mint-b was down.
	tracker.reachableMints["http://mint-a.test"] = true

	wallet := &recordingWallet{}
	m := &Merchant{
		mintHealthTracker: tracker,
		tollwallet:        wallet,
	}

	// mint-b recovers while the tollgate keeps running.
	tracker.reachableMints["http://mint-b.test"] = true

	m.AdmitReachableMints()

	seen := map[string]bool{}
	for _, u := range wallet.accepted {
		seen[u] = true
	}
	if !seen["http://mint-b.test"] {
		t.Fatalf("late mint not admitted to the wallet: %v", wallet.accepted)
	}
	if !seen["http://mint-a.test"] {
		t.Fatalf("boot-reachable mint missing from admission: %v", wallet.accepted)
	}
}

// Mints that stay unreachable must not be admitted — admission follows
// the tracker's reachable set, not the config's accepted list.
func TestAdmitReachableMintsSkipsStillUnreachableMint(t *testing.T) {
	config := mintConfigWithURLs("http://mint-a.test", "http://mint-b.test")
	tracker := newTestTracker(config, nil)
	tracker.reachableMints["http://mint-a.test"] = true

	wallet := &recordingWallet{}
	m := &Merchant{
		mintHealthTracker: tracker,
		tollwallet:        wallet,
	}

	m.AdmitReachableMints()

	for _, u := range wallet.accepted {
		if u == "http://mint-b.test" {
			t.Fatalf("unreachable mint admitted: %v", wallet.accepted)
		}
	}
}
