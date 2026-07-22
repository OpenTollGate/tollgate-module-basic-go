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

// TestNextLightningBackoffDoublingAndCap exercises the helper directly,
// covering the doubling progression, the cap, and idempotence at the cap.
func TestNextLightningBackoffDoublingAndCap(t *testing.T) {
	base := lightningQuoteMonitorInterval
	max := lightningQuoteMonitorMaxBackoff

	backoff := base
	steps := 0
	for backoff < max && steps < 20 {
		prev := backoff
		backoff = nextLightningBackoff(backoff)
		if backoff <= prev {
			t.Fatalf("backoff did not increase: %v -> %v", prev, backoff)
		}
		steps++
	}

	if backoff != max {
		t.Fatalf("expected to reach max %v, got %v after %d steps", max, backoff, steps)
	}
	if steps < 2 {
		t.Errorf("expected at least 2 steps to reach max backoff, got %d", steps)
	}

	// Once at the cap, further calls stay at the cap.
	for i := 0; i < 5; i++ {
		if got := nextLightningBackoff(max); got != max {
			t.Errorf("backoff at cap should stay at %v, got %v", max, got)
		}
	}
}

// TestJitterSleepRange verifies jitterSleep actually blocks for d plus
// a jitter in [0, maxJitter). We allow a scheduling slack of 50ms.
func TestJitterSleepRange(t *testing.T) {
	const slack = 50 * time.Millisecond
	base := 100 * time.Millisecond

	for i := 0; i < 20; i++ {
		start := time.Now()
		jitterSleep(base)
		elapsed := time.Since(start)

		if elapsed < base {
			t.Errorf("jitterSleep slept %v, expected >= %v", elapsed, base)
		}
		upper := base + lightningQuoteMonitorMaxJitter + slack
		if elapsed > upper {
			t.Errorf("jitterSleep slept %v, expected <= %v (base + maxJitter + slack)", elapsed, upper)
		}
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
	quoteIDs := []string{"q1", "q2"}
	for _, qID := range quoteIDs {
		go func(id string) {
			m.monitorLightningQuote(id)
			done.Add(1)
		}(qID)
	}

	time.Sleep(2 * time.Second)
	if done.Load() < 1 {
		t.Fatal("at least one monitor should have exited")
	}
}
