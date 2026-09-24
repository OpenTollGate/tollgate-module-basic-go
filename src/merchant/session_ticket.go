package merchant

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
)

// Session tickets and the meter-carrying rebind
// (docs/architecture/session-ticket-decision.md).
//
// A client's MAC is the module's session key, its byte meter's key and the
// gate's delivery address at once, so a device that rotates its private address
// arrives as a different customer and loses the session it paid for — and the
// obvious rebuild of that session record is a metering hole (a fresh baseline
// per address, and a `StartTime` rewritten on every "re-grant").
//
// A ticket is the structural fix: a server-signed, memory-only HANDLE onto a
// session, whose binding to a MAC is a DELIVERY ADDRESS that a rebind can move.
// Two properties are deliberate:
//
//   - The ticket carries a handle and nothing else — no allotment, no metric, no
//     MAC. It names a session; it never contains one. A ticket carrying the
//     entitlement would be a portable credential worth stealing, and a second
//     copy of state that has to stay in agreement with the session record.
//   - The signing key is drawn from crypto/rand at startup and written nowhere,
//     and the attachment store is process memory. A restart therefore
//     invalidates every ticket by construction. That is a documented behaviour
//     the portal's resume flow covers, not an accident: the customer's access
//     lives in the session record, the gate and the meter, none of which are in
//     the ticket.

const (
	// sessionTicketVersion prefixes the envelope. Versioning the format is what
	// lets a future payload change be detected and refused instead of misread.
	sessionTicketVersion = "v1"

	// defaultSessionTicketTTL bounds how long a ticket is accepted. A ticket is
	// a handle, not a grant, so its lifetime is independent of the session's: an
	// expired ticket costs the client one more request, never paid-for access.
	defaultSessionTicketTTL = 12 * time.Hour

	// maxSessionAttachments caps the in-memory handle store: expired attachments
	// are swept first, and the oldest attachment is evicted if the store is still
	// over the cap, so a busy router cannot grow it without bound.
	maxSessionAttachments = 4096
)

var (
	// ErrTicketMalformed reports an envelope that is not a ticket at all: wrong
	// version, wrong shape, or a payload that is not the signed JSON.
	ErrTicketMalformed = errors.New("session ticket malformed")
	// ErrTicketSignature reports a ticket whose signature does not verify under
	// this process's key. After a restart every previously issued ticket lands
	// here, which is the intended memory-only behaviour.
	ErrTicketSignature = errors.New("session ticket signature invalid")
	// ErrTicketExpired reports a ticket past its own expiry.
	ErrTicketExpired = errors.New("session ticket expired")
	// ErrTicketUnknown reports a correctly signed ticket whose handle this
	// process has no attachment for — or whose attachment no longer names the
	// session it was issued for (the session was retired and this MAC bought a
	// new one). A ticket names one session, and only that session.
	ErrTicketUnknown = errors.New("session ticket unknown to this process")
	// ErrTicketNoSession reports a request for a ticket by a client with no
	// session: there is nothing to attach a ticket to, and a ticket is never a
	// grant in its own right.
	ErrTicketNoSession = errors.New("no session for this client to issue a ticket for")
	// ErrAttachmentActive reports a rebind refused because the address the
	// session is currently delivered to is still authenticated. That refusal is
	// the only thing that separates a rotation from a second delivery of one
	// session.
	ErrAttachmentActive = errors.New("the previous attachment is still authenticated")
)

// SessionTicket is the signed payload of a ticket: a handle and its lifetime.
type SessionTicket struct {
	Handle    string `json:"handle"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

// VerifiedSessionTicket is what a verified ticket says: the handle it carries,
// and the delivery address that handle currently names.
type VerifiedSessionTicket struct {
	Handle     string
	IssuedAt   int64
	ExpiresAt  int64
	MacAddress string
}

// sessionAttachment is the binding of one session handle to the MAC its access
// is delivered to. `AttachedAt` moves on every rebind; the handle does not.
type sessionAttachment struct {
	Handle     string
	MacAddress string
	IssuedAt   int64
	ExpiresAt  int64
	AttachedAt int64
}

// ticketSigner holds this process's ticket key. The key is never persisted and
// never logged: it is the only thing standing between a ticket and a payload
// anybody can write.
type ticketSigner struct {
	mu  sync.Mutex
	key []byte
}

func newTicketSigner() (*ticketSigner, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("session ticket signer: %w", err)
	}
	return &ticketSigner{key: key}, nil
}

// sign authenticates the envelope body — version and payload exactly as they
// travel, so a re-encoding cannot change what was signed.
func (s *ticketSigner) sign(body string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *ticketSigner) issue(handle string, now time.Time, ttl time.Duration) (string, SessionTicket, error) {
	ticket := SessionTicket{
		Handle:    handle,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(ttl).Unix(),
	}

	payload, err := json.Marshal(ticket)
	if err != nil {
		return "", SessionTicket{}, fmt.Errorf("session ticket payload: %w", err)
	}

	body := sessionTicketVersion + "." + base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + s.sign(body), ticket, nil
}

func (s *ticketSigner) verify(ticket string, now time.Time) (SessionTicket, error) {
	var payload SessionTicket

	parts := strings.Split(ticket, ".")
	if len(parts) != 3 || parts[0] != sessionTicketVersion {
		return payload, ErrTicketMalformed
	}

	body := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(s.sign(body)), []byte(parts[2])) {
		return payload, ErrTicketSignature
	}

	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return payload, ErrTicketMalformed
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Handle == "" {
		return payload, ErrTicketMalformed
	}
	if now.Unix() >= payload.ExpiresAt {
		return payload, ErrTicketExpired
	}

	return payload, nil
}

// ticketState is the merchant's memory-only ticket state: the process key, the
// handle → attachment store, and the clock/TTL a test can shrink.
type ticketState struct {
	mu          sync.Mutex
	signer      *ticketSigner
	attachments map[string]*sessionAttachment
	now         func() time.Time
	ttl         time.Duration
}

// ticketStateLocked returns the ticket state, creating the signer on first use
// so a merchant that never issues a ticket never draws a key. Callers must hold
// no other merchant lock; see the lock order note on RebindSession.
func (m *Merchant) ticketState() (*ticketState, error) {
	m.ticketMu.Lock()
	defer m.ticketMu.Unlock()

	if m.tickets == nil {
		signer, err := newTicketSigner()
		if err != nil {
			return nil, err
		}
		m.tickets = &ticketState{
			signer:      signer,
			attachments: make(map[string]*sessionAttachment),
			now:         time.Now,
			ttl:         defaultSessionTicketTTL,
		}
	}

	return m.tickets, nil
}

// newTicketHandle returns a fresh opaque handle. It is random rather than
// derived from the MAC or the session, so a handle tells a reader nothing about
// the customer it names.
func newTicketHandle() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("session ticket handle: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// sweepLocked drops expired attachments, and then — if the store is still over
// its cap — the oldest ones. Caller must hold ts.mu.
func (ts *ticketState) sweepLocked(now time.Time) {
	for handle, attachment := range ts.attachments {
		if now.Unix() >= attachment.ExpiresAt {
			delete(ts.attachments, handle)
		}
	}

	for len(ts.attachments) >= maxSessionAttachments {
		oldestHandle := ""
		oldest := now.Unix()
		for handle, attachment := range ts.attachments {
			if attachment.AttachedAt < oldest {
				oldest, oldestHandle = attachment.AttachedAt, handle
			}
		}
		if oldestHandle == "" {
			return
		}
		delete(ts.attachments, oldestHandle)
	}
}

// attachmentLocked returns the live attachment for handle, refusing an expired
// one. Caller must hold ts.mu.
func (ts *ticketState) attachmentLocked(handle string, now time.Time) (*sessionAttachment, error) {
	attachment, exists := ts.attachments[handle]
	if !exists {
		return nil, ErrTicketUnknown
	}
	if now.Unix() >= attachment.ExpiresAt {
		delete(ts.attachments, handle)
		return nil, ErrTicketExpired
	}
	return attachment, nil
}

// IssueSessionTicket binds a ticket to the session of macAddress and returns the
// signed ticket plus its expiry.
//
// Nothing is issued for a client with no session: the ticket is a handle onto a
// record that already exists, never a grant. Issuing twice for one session
// returns tickets for the SAME handle — a second tab must not invalidate the
// first tab's ticket — and refreshes the attachment's expiry.
func (m *Merchant) IssueSessionTicket(macAddress string) (string, int64, error) {
	macAddress = NormalizeMACAddress(macAddress)

	ts, err := m.ticketState()
	if err != nil {
		return "", 0, err
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()

	now := ts.now()

	// Lock order: ts.mu, then sessionMu. Nothing in this package takes them the
	// other way round (the usage monitor and the expiry paths hold sessionMu and
	// never touch the ticket store).
	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()

	session, exists := m.customerSessions[macAddress]
	if !exists || sessionHasExpired(session, now) {
		return "", 0, ErrTicketNoSession
	}

	handle := session.ticketHandle
	if handle == "" {
		handle, err = newTicketHandle()
		if err != nil {
			return "", 0, err
		}
		session.ticketHandle = handle
	}

	ticket, payload, err := ts.signer.issue(handle, now, ts.ttl)
	if err != nil {
		return "", 0, err
	}

	ts.sweepLocked(now)
	ts.attachments[handle] = &sessionAttachment{
		Handle:     handle,
		MacAddress: macAddress,
		IssuedAt:   payload.IssuedAt,
		ExpiresAt:  payload.ExpiresAt,
		AttachedAt: now.Unix(),
	}

	return ticket, payload.ExpiresAt, nil
}

// VerifySessionTicket verifies a ticket and reports the handle it carries and
// the delivery address that handle currently names.
//
// It grants nothing by itself. A verified ticket says which session is asking;
// what may be done with that answer is decided by the caller — a rebind, for
// instance, is decided by the socket's own address and by whether the address
// the session currently delivers to is still authenticated.
func (m *Merchant) VerifySessionTicket(ticket string) (*VerifiedSessionTicket, error) {
	ts, err := m.ticketState()
	if err != nil {
		return nil, err
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()

	now := ts.now()

	payload, err := ts.signer.verify(ticket, now)
	if err != nil {
		return nil, err
	}

	attachment, err := ts.attachmentLocked(payload.Handle, now)
	if err != nil {
		return nil, err
	}

	return &VerifiedSessionTicket{
		Handle:     payload.Handle,
		IssuedAt:   payload.IssuedAt,
		ExpiresAt:  attachment.ExpiresAt,
		MacAddress: attachment.MacAddress,
	}, nil
}

// RebindSession moves the session a ticket names to macAddress — the rotation
// path — and returns the moved session.
//
// Three invariants hold here, and each one is a way the obvious implementation
// hands out free access (docs/architecture/session-ticket-decision.md):
//
//  1. the byte meter CARRIES: the moved record keeps the total the session has
//     already consumed, and the new attachment's baseline measures only that
//     attachment. A rebind never starts a fresh allotment;
//  2. StartTime is PRESERVED: a rebind is not a renewal. Recomputing StartTime
//     the way AddAllotment does for a renewal would hand back the time the
//     customer has already spent;
//  3. the rebind is REFUSED while the old attachment is still authenticated
//     (ErrAttachmentActive) — the rule that separates a rotation from a copied
//     ticket delivered to a second device.
func (m *Merchant) RebindSession(ticket, macAddress string) (*CustomerSession, error) {
	macAddress = NormalizeMACAddress(macAddress)

	ts, err := m.ticketState()
	if err != nil {
		return nil, err
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()

	now := ts.now()

	payload, err := ts.signer.verify(ticket, now)
	if err != nil {
		return nil, err
	}

	attachment, err := ts.attachmentLocked(payload.Handle, now)
	if err != nil {
		return nil, err
	}

	previous := attachment.MacAddress

	// Invariant 3, before any state moves: a session may be delivered to exactly
	// one live address at a time. The probe is read-only and changes nothing;
	// when it cannot answer at all (ndsctl unreachable) the rebind proceeds,
	// because failing closed would strand a legitimate rotation on a flaky
	// daemon — the gap is stated in the ADR rather than hidden.
	if previous != macAddress {
		state, probeErr := ndsClientCheck(previous)
		if probeErr == nil && state.Authenticated {
			return nil, fmt.Errorf("%w: %s", ErrAttachmentActive, previous)
		}
		if probeErr != nil {
			log.Printf("WARNING: session rebind for handle %s: could not confirm that the previous attachment %s is gone (%v) — proceeding on an unanswerable identity probe",
				payload.Handle, previous, probeErr)
		}
	}

	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()

	session, exists := m.customerSessions[previous]
	if !exists || session.ticketHandle != payload.Handle {
		// A handle names one session, and only the one it was issued for: a
		// session retired and re-bought under the same MAC is a different
		// session, and an old ticket must not be able to move it.
		return nil, ErrTicketUnknown
	}

	if previous == macAddress {
		// Already delivering here: report the session and change nothing.
		return cloneCustomerSession(session), nil
	}

	// Invariant 1: carry the meter. The carried total is what the session already
	// consumed — its carried-in total plus the highest reading seen for the
	// attachment it is leaving. The attachment's counters may still be readable
	// for a moment (its client record outlives its deauth), so take the higher of
	// the live reading and the recorded one: a rotation must never be able to
	// walk the meter backwards.
	carried := session.Consumed
	if session.Metric == "bytes" {
		carried = session.Consumed + session.attachmentUsage
		if since, readErr := valve.GetDataUsageSinceBaseline(previous); readErr == nil && since > session.attachmentUsage {
			carried = session.Consumed + since
		}
	}

	// Invariant 2: StartTime is copied, never recomputed.
	moved := &CustomerSession{
		MacAddress:   macAddress,
		StartTime:    session.StartTime,
		Metric:       session.Metric,
		Allotment:    session.Allotment,
		Consumed:     carried,
		ticketHandle: payload.Handle,
	}

	delete(m.customerSessions, previous)
	m.customerSessions[macAddress] = moved

	attachment.MacAddress = macAddress
	attachment.AttachedAt = now.Unix()

	// The new attachment is metered against its own baseline, which is what the
	// carried total sits on top of. If NoDogSplash cannot report its counters yet
	// the baseline is recorded from zero — that under-counts this attachment's
	// traffic until the next reading, and it is the honest direction to err in,
	// but it must never silently drop the carried total (it does not: `carried`
	// is already on the record).
	if moved.Metric == "bytes" {
		if err := valve.SetDataBaseline(macAddress); err != nil {
			log.Printf("WARNING: session rebind for handle %s: could not read the counters of the new attachment %s, so its metering baseline is recorded from zero on top of the %d bytes already consumed: %v",
				payload.Handle, macAddress, carried, err)
		}
	}

	log.Printf("Session rebind: handle %s moved from %s to %s (%d bytes carried, metric=%s)",
		payload.Handle, previous, macAddress, carried, moved.Metric)

	return cloneCustomerSession(moved), nil
}
