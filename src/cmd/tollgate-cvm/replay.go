package main

import (
	"fmt"
	"time"
)

// Freshness and duplicate-execution guards (audit Q1).
//
// The signature model makes the CALLER's identity unforgeable, so an attacker
// cannot mint a request as the maintainer. But the maintainer's own requests
// travel over public relays, and a hostile relay (or anyone who recorded the
// traffic) can re-broadcast a captured kind-1059 gift wrap. Without these
// guards every write tool is re-executable forever, and one captured
// `config_set maintainer_npub <old value>` silently reverts the router's owner
// — a permanent hijack of the write path.
//
// Two guards, deliberately both cheap and in-memory:
//
//   - CheckFreshness rejects events outside a time window, which kills
//     capture-and-replay-LATER (the ordering half of the attack).
//   - SeenSet drops a repeat delivery of the same event id, which kills
//     duplicate delivery by a relay or a re-broadcast.
//
// What this does NOT cover: a replay after a process restart inside the
// freshness window. Closing that needs persistence (the ADR's next step, in the
// spirit of src/cli/drain_journal.go); it is recorded rather than pretended.

const (
	// freshPast is how old an inbound request may be.
	freshPast = 120 * time.Second
	// freshFuture is how far ahead a clock-skewed peer may be.
	freshFuture = 60 * time.Second
)

// nostrTimestamp converts a time to a Nostr created_at (unix seconds).
func nostrTimestamp(t time.Time) int64 { return t.Unix() }

// CheckFreshness rejects an inbound event whose created_at is outside the
// accepted window. A zero timestamp is refused: it means the field was absent,
// and an absent clock cannot be reasoned about.
func CheckFreshness(createdAt int64, now time.Time) error {
	if createdAt == 0 {
		return fmt.Errorf("request has no timestamp (created_at absent)")
	}
	age := now.Sub(time.Unix(createdAt, 0))
	if age > freshPast {
		return fmt.Errorf("request is stale (%s old, limit %s) — refusing so a captured request cannot be replayed later", age.Round(time.Second), freshPast)
	}
	if age < -freshFuture {
		return fmt.Errorf("request is %s in the future (limit %s) — refusing", (-age).Round(time.Second), freshFuture)
	}
	return nil
}

// SeenSet is a bounded, TTL'd set of event ids already dispatched.
//
// Bounded on purpose: an unbounded set is a memory leak on a router with a
// few hundred MB of RAM, and a leak is a denial of service on a payment
// gateway. When the cap is reached the oldest entries are evicted, which means
// the guarantee degrades gracefully (an extremely old duplicate could land)
// instead of the process dying.
type SeenSet struct {
	cap     int
	ttl     time.Duration
	entries map[string]time.Time
	order   []string // insertion order, for eviction
}

func NewSeenSet(capacity int, ttl time.Duration) *SeenSet {
	if capacity <= 0 {
		capacity = 1024
	}
	return &SeenSet{cap: capacity, ttl: ttl, entries: make(map[string]time.Time, capacity)}
}

// Seen records id and reports whether it had already been seen. It both tests
// and records in one call so a caller cannot forget to record.
func (s *SeenSet) Seen(id string, now time.Time) bool {
	if id == "" {
		// No id to dedupe on. Treat as seen: the caller cannot have a
		// legitimate id-less event (gift wraps always have one), so refusing
		// is fail-closed.
		return true
	}
	if at, ok := s.entries[id]; ok && now.Sub(at) < s.ttl {
		return true
	}
	s.entries[id] = now
	s.order = append(s.order, id)
	s.evict(now)
	return false
}

func (s *SeenSet) evict(now time.Time) {
	// Drop expired entries from the front, then trim to the cap.
	kept := s.order[:0]
	for _, id := range s.order {
		at, ok := s.entries[id]
		if !ok {
			continue
		}
		if now.Sub(at) >= s.ttl {
			delete(s.entries, id)
			continue
		}
		kept = append(kept, id)
	}
	s.order = kept

	for len(s.order) > s.cap {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.entries, oldest)
	}
}

// Len reports the current entry count (for tests and diagnostics).
func (s *SeenSet) Len() int { return len(s.entries) }
