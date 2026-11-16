package multi_wan

import (
	"math"
	"sync"
	"testing"
	"time"
)

func TestSamplerWindowAndAggregates(t *testing.T) {
	now := time.Unix(100, 0)
	s := NewSampler(10*time.Second, func() time.Time { return now })
	s.SetUp("wan", true)
	for i, rtt := range []float64{30, 10, 20} {
		at := now.Add(time.Duration(i-2) * 5 * time.Second)
		if err := s.Add("wan", Sample{At: at, Bytes: uint64(i) * 1250000, RTTMs: rtt, LossPct: float64(i) * 10, DownMbps: 999, UpMbps: 999}); err != nil {
			t.Fatal(err)
		}
	}
	m := s.Measurements()["wan"]
	if !m.Up || m.RecentSamples != 3 || m.DownMbps != 2 || m.RTTMs != 20 || m.LossPct != 10 || m.SessionCost != nil {
		t.Fatalf("aggregate: %+v", m)
	}
	now = now.Add(time.Nanosecond)
	m = s.Measurements()["wan"]
	if m.RecentSamples != 2 || m.RTTMs != 15 || m.LossPct != 15 || m.DownMbps != 2 {
		t.Fatalf("boundary: %+v", m)
	}
	now = now.Add(10 * time.Second)
	s.Prune()
	m = s.Measurements()["wan"]
	if m.Up || m.RecentSamples != 0 || m.DownMbps != 0 || m.RTTMs != 0 || m.LossPct != 0 {
		t.Fatalf("expired: %+v", m)
	}
	s.SetUp("wan", false)
	if s.Measurements()["wan"].Up {
		t.Fatal("SetUp false ignored")
	}
}

func TestSamplerCounterResetAndLargeCounter(t *testing.T) {
	now := time.Unix(100, 0)
	s := NewSampler(time.Minute, func() time.Time { return now })
	counters := []uint64{math.MaxUint64 - 1250000, math.MaxUint64, 0, 1250000}
	for i, b := range counters {
		if err := s.Add("wan", Sample{At: now.Add(time.Duration(i-3) * time.Second), Bytes: b}); err != nil {
			t.Fatal(err)
		}
	}
	// The reset interval has unknown traffic and is excluded, not subtracted
	// unsigned, counted as a wrap, or averaged as a zero-rate observation.
	if m := s.Measurements()["wan"]; m.DownMbps != 10 || m.RecentSamples != 4 {
		t.Fatalf("reset: %+v", m)
	}
}

func TestSamplerRejectsBadSamples(t *testing.T) {
	now := time.Unix(100, 0)
	s := NewSampler(time.Minute, func() time.Time { return now })
	if err := s.Add("wan", Sample{At: now}); err != nil {
		t.Fatal(err)
	}
	bad := []Sample{
		{At: now.Add(-time.Second)}, {At: now}, {At: now.Add(time.Second)},
		{RTTMs: -1}, {RTTMs: math.NaN()}, {LossPct: 101}, {LossPct: -1},
		{LossPct: math.Inf(1)}, {DownMbps: -1}, {UpMbps: math.NaN()},
	}
	for _, sample := range bad {
		if err := s.Add("wan", sample); err == nil {
			t.Errorf("accepted %+v", sample)
		}
	}
	if err := s.Add("", Sample{}); err == nil {
		t.Fatal("accepted empty id")
	}
	if got := s.Measurements()["wan"].RecentSamples; got != 1 {
		t.Fatalf("invalid samples changed count: %d", got)
	}
	now = now.Add(2 * time.Minute)
	s.Prune()
	if err := s.Add("wan", Sample{At: time.Unix(100, 0)}); err == nil {
		t.Fatal("accepted stale sample after prune")
	}
}

func TestSamplerBackwardClock(t *testing.T) {
	now := time.Unix(100, 0)
	s := NewSampler(time.Second, func() time.Time { return now })
	if err := s.Add("wan", Sample{}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	s.Prune()
	now = now.Add(-time.Second)
	if err := s.Add("other", Sample{}); err == nil {
		t.Fatal("accepted backward clock")
	}
	s.Prune()
	if got := s.Measurements()["wan"].RecentSamples; got != 0 {
		t.Fatalf("clock rollback resurrected samples: %d", got)
	}
	now = now.Add(time.Second)
	if err := s.Add("wan", Sample{}); err != nil {
		t.Fatal(err)
	}
}

func TestSamplerIsolationAndSingleSample(t *testing.T) {
	now := time.Unix(100, 0)
	s := NewSampler(time.Minute, func() time.Time { return now })
	if len(s.Measurements()) != 0 {
		t.Fatal("nonempty initial measurements")
	}
	if err := s.Add("wan", Sample{Bytes: 1000000, DownMbps: 99, UpMbps: 88, RTTMs: 7}); err != nil {
		t.Fatal(err)
	}
	m := s.Measurements()
	if m["wan"].DownMbps != 0 || m["wan"].Up || m["wan"].RTTMs != 7 {
		t.Fatalf("single: %+v", m)
	}
	m["wan"] = Measurement{Up: true}
	delete(m, "wan")
	if s.Measurements()["wan"].RecentSamples != 1 {
		t.Fatal("returned map aliases state")
	}
	s.SetUp("other", true)
	if s.Measurements()["other"].RecentSamples != 0 {
		t.Fatal("cross-link contamination")
	}
}

func TestSamplerConstructor(t *testing.T) {
	for _, tc := range []struct {
		window time.Duration
		now    func() time.Time
	}{
		{0, func() time.Time { return time.Time{} }}, {-1, func() time.Time { return time.Time{} }}, {time.Second, nil},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("expected invalid configuration panic")
				}
			}()
			NewSampler(tc.window, tc.now)
		}()
	}
}

func TestSamplerConcurrentAccess(t *testing.T) {
	now := time.Unix(100, 0)
	s := NewSampler(time.Minute, func() time.Time { return now })
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				s.SetUp("wan", true)
				_ = s.Add("wan", Sample{})
				s.Measurements()
				s.Prune()
			}
		}()
	}
	wg.Wait()
	if s.Measurements()["wan"].RecentSamples != 1 {
		t.Fatal("duplicate timestamps accepted")
	}
}
