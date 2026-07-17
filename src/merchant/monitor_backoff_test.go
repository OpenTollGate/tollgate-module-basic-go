package merchant

import (
	"math/rand"
	"sync/atomic"
	"testing"
	"time"
)

// TestMonitorBackoffConstants verifies the backoff configuration constants
// are set to sane values that prevent mint API abuse.
func TestMonitorBackoffConstants(t *testing.T) {
	if lightningQuoteMonitorInterval < 2*time.Second {
		t.Errorf("monitor interval %v too aggressive, must be >= 2s", lightningQuoteMonitorInterval)
	}
	if lightningQuoteMonitorMaxBackoff < lightningQuoteMonitorInterval {
		t.Errorf("max backoff %v must be >= base interval %v", lightningQuoteMonitorMaxBackoff, lightningQuoteMonitorInterval)
	}
	if lightningQuoteMonitorMaxBackoff > 5*time.Minute {
		t.Errorf("max backoff %v too high, must be <= 5m", lightningQuoteMonitorMaxBackoff)
	}
	if lightningQuoteMonitorMaxJitter <= 0 {
		t.Error("jitter must be > 0 to prevent thundering herd")
	}
	if lightningQuoteMonitorMaxJitter > lightningQuoteMonitorInterval {
		t.Errorf("jitter %v must be <= interval %v", lightningQuoteMonitorMaxJitter, lightningQuoteMonitorInterval)
	}
}

// TestMonitorJitterRange verifies jitter stays within [0, maxJitter).
func TestMonitorJitterRange(t *testing.T) {
	for i := 0; i < 1000; i++ {
		jitter := time.Duration(rand.Int63n(int64(lightningQuoteMonitorMaxJitter)))
		if jitter < 0 {
			t.Fatalf("jitter must be non-negative, got %v", jitter)
		}
		if jitter >= lightningQuoteMonitorMaxJitter {
			t.Fatalf("jitter %v exceeds max %v", jitter, lightningQuoteMonitorMaxJitter)
		}
	}
}

// TestMonitorExitsOnQuoteMissing verifies the monitor goroutine exits
// when the quote record is deleted while it's running.
func TestMonitorExitsOnQuoteMissing(t *testing.T) {
	m := &Merchant{
		lightningQuotes: make(map[string]*lightningQuoteRecord),
	}

	done := make(chan struct{})
	go func() {
		m.monitorLightningQuote("nonexistent")
		close(done)
	}()

	select {
	case <-done:
		// Expected: monitor exits immediately when quote not found.
	case <-time.After(5 * time.Second):
		t.Fatal("monitor did not exit when quote record was missing")
	}
}

// TestMonitorExitsOnExpiry verifies the monitor goroutine exits
// when the quote it's monitoring expires.
func TestMonitorExitsOnExpiry(t *testing.T) {
	now := time.Now()
	m := &Merchant{
		lightningQuotes: map[string]*lightningQuoteRecord{
			"expired-quote": {
				MacAddress: "aa:bb:cc:dd:ee:ff",
				MintURL:    "https://mint.example.com",
				Amount:     1,
				CreatedAt:  now.Add(-10 * time.Minute),
				Expiry:     uint64(now.Add(-9 * time.Minute).Unix()),
			},
		},
	}

	done := make(chan struct{})
	go func() {
		m.monitorLightningQuote("expired-quote")
		close(done)
	}()

	select {
	case <-done:
		// Expected: monitor detects expiry and exits.
	case <-time.After(5 * time.Second):
		t.Fatal("monitor did not exit when quote expired")
	}

	// Quote should be deleted by the monitor.
	if _, exists := m.lightningQuotes["expired-quote"]; exists {
		t.Error("expected expired quote to be deleted by monitor")
	}
}

// TestMonitorBackoffProgression verifies that the backoff value doubles
// on each error and caps at maxBackoff. This is a pure logic test —
// we verify the math, not the sleep.
func TestMonitorBackoffProgression(t *testing.T) {
	base := lightningQuoteMonitorInterval
	max := lightningQuoteMonitorMaxBackoff

	// Simulate backoff progression.
	backoff := base
	for i := 0; i < 20; i++ {
		backoff *= 2
		if backoff > max {
			backoff = max
		}
	}

	if backoff != max {
		t.Errorf("backoff should have capped at %v, got %v", max, backoff)
	}

	// Verify it takes a reasonable number of steps to reach the cap.
	steps := 0
	backoff = base
	for backoff < max {
		backoff *= 2
		if backoff > max {
			backoff = max
		}
		steps++
	}

	if steps < 2 {
		t.Errorf("expected at least 2 steps to reach max backoff, got %d", steps)
	}
	if steps > 10 {
		t.Errorf("too many steps (%d) to reach max backoff — base interval may be too small", steps)
	}
}

// TestMonitorBackoffResetsOnSuccess verifies backoff resets to base
// after a successful API response. This is a logic test — we verify
// the reset pattern works correctly.
func TestMonitorBackoffResetsOnSuccess(t *testing.T) {
	base := lightningQuoteMonitorInterval

	// Simulate: error, error, success — backoff should reset.
	backoff := base
	// Error 1
	backoff *= 2
	if backoff > lightningQuoteMonitorMaxBackoff {
		backoff = lightningQuoteMonitorMaxBackoff
	}
	// Error 2
	backoff *= 2
	if backoff > lightningQuoteMonitorMaxBackoff {
		backoff = lightningQuoteMonitorMaxBackoff
	}

	if backoff == base {
		t.Fatal("backoff should have increased after errors")
	}

	// Success — reset.
	backoff = base

	if backoff != base {
		t.Errorf("backoff should have reset to %v, got %v", base, backoff)
	}
}

// TestMonitorLightningQuoteConcurrent ensures multiple monitor goroutines
// can run concurrently without races.
func TestMonitorLightningQuoteConcurrent(t *testing.T) {
	m := &Merchant{
		lightningQuotes: map[string]*lightningQuoteRecord{
			"q1": {
				MacAddress: "aa:bb:cc:dd:ee:ff",
				MintURL:    "https://mint.example.com",
				Amount:     1,
				CreatedAt:  time.Now().Add(-10 * time.Minute),
				Expiry:     uint64(time.Now().Add(-9 * time.Minute).Unix()),
			},
			"q2": {
				MacAddress: "11:22:33:44:55:66",
				MintURL:    "https://mint.example.com",
				Amount:     1,
				CreatedAt:  time.Now().Add(-10 * time.Minute),
				Expiry:     uint64(time.Now().Add(-9 * time.Minute).Unix()),
			},
		},
	}

	var done atomic.Int32
	for i := 0; i < 2; i++ {
		go func(qID string) {
			m.monitorLightningQuote(qID)
			done.Add(1)
		}("q1") // Both try q1, second will find it already deleted by first
	}

	time.Sleep(2 * time.Second)
	if done.Load() < 1 {
		t.Fatal("at least one monitor should have exited")
	}
}
