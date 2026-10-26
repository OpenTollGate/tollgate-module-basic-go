package multi_wan

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Sample is one observation. Bytes is a cumulative download byte counter;
// callers must supply the same counter for each observation of an uplink.
// DownMbps and UpMbps are accepted for input compatibility but not aggregated:
// throughput is derived from Bytes. One counter cannot identify both directions,
// and Measurement has no upload field; upload throughput is not inferred.
// A zero At is stamped using the injected clock.
type Sample struct {
	At                               time.Time
	DownMbps, UpMbps, RTTMs, LossPct float64
	Bytes                            uint64
}

type sampledLink struct {
	up      bool
	samples []Sample
	lastAt  time.Time
	hasLast bool
}

// Sampler maintains independent rolling windows without probes or background
// goroutines. All methods are concurrency-safe. Do not copy a Sampler after use.
// Health defaults to down and is changed only by SetUp, never by Add.
type Sampler struct {
	mu      sync.Mutex
	window  time.Duration
	now     func() time.Time
	lastNow time.Time
	hasNow  bool
	links   map[string]*sampledLink
}

// NewSampler requires a positive window and a non-nil clock, and panics on
// invalid configuration. It never substitutes the wall clock for the caller's
// clock. The clock runs under the sampler lock and must not call sampler methods.
func NewSampler(window time.Duration, now func() time.Time) *Sampler {
	if window <= 0 || now == nil {
		panic("multi_wan: sampler requires positive window and clock")
	}
	return &Sampler{window: window, now: now, links: make(map[string]*sampledLink)}
}

// clockLocked retains a high-water mark: a backward clock cannot resurrect
// expired samples. Add rejects rollback; read/prune operations use this mark.
func (s *Sampler) clockLocked() (time.Time, bool) {
	now := s.now()
	if s.hasNow && now.Before(s.lastNow) {
		return s.lastNow, false
	}
	s.lastNow, s.hasNow = now, true
	return now, true
}

// Add rejects non-finite/negative metrics, loss outside 0..100, empty IDs,
// future or expired timestamps, and duplicate/backward per-link timestamps.
// Timestamp ordering survives pruning. A falling counter starts a new counter
// epoch: the crossing interval is excluded from the rate rather than assumed
// to be a wrap or interpreted as traffic. Rejected samples do not change links.
func (s *Sampler) Add(id string, sample Sample) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now, ok := s.clockLocked()
	if !ok {
		return fmt.Errorf("multi_wan: sampler clock moved backward")
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("multi_wan: empty sampler uplink ID")
	}
	for _, v := range []float64{sample.DownMbps, sample.UpMbps, sample.RTTMs, sample.LossPct} {
		if !finite(v) || v < 0 {
			return fmt.Errorf("multi_wan: sample metrics must be finite and non-negative")
		}
	}
	if sample.LossPct > 100 {
		return fmt.Errorf("multi_wan: sample loss exceeds 100 percent")
	}
	if sample.At.IsZero() {
		sample.At = now
	}
	if sample.At.After(now) || sample.At.Before(now.Add(-s.window)) {
		return fmt.Errorf("multi_wan: sample outside current window")
	}
	link := s.links[id]
	if link != nil && link.hasLast && !sample.At.After(link.lastAt) {
		return fmt.Errorf("multi_wan: sample timestamp must increase")
	}
	if link == nil {
		link = &sampledLink{}
		s.links[id] = link
	}
	link.lastAt, link.hasLast = sample.At, true
	link.samples = append(link.samples, sample)
	s.pruneLocked(now)
	return nil
}

// SetUp records externally observed link health independently of samples.
// Empty IDs are ignored. Health and timestamp high-water marks survive expiry.
func (s *Sampler) SetUp(id string, up bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(id) == "" {
		return
	}
	link := s.links[id]
	if link == nil {
		link = &sampledLink{}
		s.links[id] = link
	}
	link.up = up
}

// Prune drops observations strictly older than now-window. The cutoff itself
// belongs to the window. No pre-window counter baseline is retained or used.
func (s *Sampler) Prune() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now, _ := s.clockLocked()
	s.pruneLocked(now)
}

func (s *Sampler) pruneLocked(now time.Time) {
	cutoff := now.Add(-s.window)
	for _, link := range s.links {
		i := 0
		for i < len(link.samples) && link.samples[i].At.Before(cutoff) {
			i++
		}
		if i == len(link.samples) {
			link.samples = nil
		} else if i > 0 {
			// Release the backing storage of an expired burst, not just its prefix.
			link.samples = append([]Sample(nil), link.samples[i:]...)
		}
	}
}

// Measurements returns a detached snapshot, pruning at the injected time.
// RTT is the median and loss the arithmetic mean of retained observations.
// Download Mbps is sum(valid byte deltas)*8 / sum(valid interval seconds)/1e6;
// reset-crossing intervals are omitted. Fewer than two samples yield zero rate.
// SessionCost is always nil: pricing must come from the upstream session owner.
func (s *Sampler) Measurements() map[string]Measurement {
	s.mu.Lock()
	defer s.mu.Unlock()
	now, _ := s.clockLocked()
	s.pruneLocked(now)
	out := make(map[string]Measurement, len(s.links))
	for id, link := range s.links {
		m := Measurement{Up: link.up && len(link.samples) > 0, RecentSamples: len(link.samples)}
		if len(link.samples) > 0 {
			rtts := make([]float64, len(link.samples))
			var bytes, seconds float64
			for i, sample := range link.samples {
				rtts[i] = sample.RTTMs
				// Divide before summing to avoid overflowing a finite mean.
				m.LossPct += sample.LossPct / float64(len(link.samples))
				if i > 0 {
					prev := link.samples[i-1]
					if sample.Bytes >= prev.Bytes {
						bytes += float64(sample.Bytes - prev.Bytes)
						seconds += sample.At.Sub(prev.At).Seconds()
					}
				}
			}
			sort.Float64s(rtts)
			mid := len(rtts) / 2
			m.RTTMs = rtts[mid]
			if len(rtts)%2 == 0 {
				m.RTTMs = rtts[mid-1]/2 + rtts[mid]/2
			}
			if seconds > 0 {
				m.DownMbps = bytes * 8 / seconds / 1e6
			}
		}
		out[id] = m
	}
	return out
}
