package fips_exit

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"time"
)

// This file makes the admission rule reachable. Everything it uses is already
// tested: Challenge/Claim/Verify in claim.go, Grant/State/RenderClientsFile and
// Redeem in admission.go and redeem.go. The reason it exists is a product one.
//
// fips-exitd speaks the tunnel, not HTTP: it accepts raw IPv6 protocol 4 and 41
// on the mesh interface and has no listener at all. So there is nowhere on the
// exit for a phone to ask what access costs. Meanwhile the module's existing
// purchase surface (:2121) identifies a caller by MAC resolved from the DHCP
// lease file, which a remote FIPS peer can never have. The two halves cannot
// meet on their own.
//
// This listener is the meeting point, and it is deliberately narrow:
//
//   GET  /  returns the challenge terms (what a buyer is paying for)
//   POST /  takes a signed claim plus the terms the buyer accepts, and either
//           grants or refuses
//
// The identity decision is Redeem's, not this file's. The one thing this file
// MUST get right is where the buyer's address comes from: the transport, via
// the injected ObservedSource resolver. If that were ever read from a body
// field or a header, any caller could claim any peer's tunnel, which is the
// exact failure the claim layer was built to prevent.

// PurchaseOffer is what a buyer receives before paying: the priced terms and
// the nonce they must sign into. It is not a purchase.
type PurchaseOffer struct {
	Challenge Challenge `json:"challenge"`
}

// PurchaseRequest is one attempt to buy. TTLSeconds and ByteBudget are what the
// buyer thinks they are buying; Redeem refuses them if they exceed or contradict
// the priced challenge.
type PurchaseRequest struct {
	Claim      Claim  `json:"claim"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
	ByteBudget uint64 `json:"byte_budget,omitempty"`
}

// SourceResolver reports the address the transport says this caller is. It is
// the only acceptable origin for the binding check, and it is injected so that
// a test can prove the handler does not consult the request for it.
type SourceResolver func(*http.Request) (string, error)

// PurchaseConfig is everything the service needs. Every field is explicit: this
// type is constructed in tests and on a router, and a zero value that silently
// grants would be a money bug, so NewPurchaseService validates instead.
type PurchaseConfig struct {
	// Now is the clock. Required.
	Now func() time.Time
	// ExitKey is this exit's identity, carried in every challenge. Required.
	ExitKey PeerKey
	// PriceSats and Mode are the terms offered. Mode is Required.
	PriceSats uint64
	Mode      BillingMode
	// TTL is how long an offered challenge stays valid. Zero means ten minutes.
	TTL time.Duration
	// Store persists admission state. Required for POST.
	Store Store
	// ClientsStore persists the rendered clients file the exit daemon reads. When
	// nil the file is not written, which is only ever right in a test.
	ClientsStore Store
	// Cache is the replay guard. Required for POST.
	Cache *NonceCache
	// Random supplies challenge nonces. Defaults to crypto/rand.Reader; tests
	// inject a deterministic reader instead of reaching into the nonce itself.
	Random io.Reader
	// ObservedSource resolves the caller's transport address. Required for POST.
	ObservedSource SourceResolver
}

// PurchaseService serves the two requests a buyer makes.
type PurchaseService struct {
	cfg PurchaseConfig
}

// NewPurchaseService validates the configuration. It refuses to build a service
// that could grant on incomplete authority, because such a service fails open
// the first time someone constructs it in a new place.
func NewPurchaseService(cfg PurchaseConfig) *PurchaseService {
	return &PurchaseService{cfg: cfg}
}

// Store exposes the state store, so an embedding daemon can render the clients
// file from the same durable state the service writes.
func (s *PurchaseService) Store() Store { return s.cfg.Store }

// Handler returns the HTTP handler for the purchase surface.
func (s *PurchaseService) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.serve)
	return mux
}

func (s *PurchaseService) serve(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.serveOffer(w, r)
	case http.MethodPost:
		s.servePurchase(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeProblem(w, http.StatusMethodNotAllowed, "method", "use GET for terms or POST to buy")
	}
}

func (s *PurchaseService) serveOffer(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Now == nil {
		writeProblem(w, http.StatusInternalServerError, "unconfigured", "this exit cannot issue terms")
		return
	}
	ttl := s.cfg.TTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	random := s.cfg.Random
	if random == nil {
		random = rand.Reader
	}
	now := s.cfg.Now()
	ch, err := NewChallenge(random, now, ttl, s.cfg.ExitKey, s.cfg.PriceSats, s.cfg.Mode)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "challenge", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, PurchaseOffer{Challenge: ch})
}

func (s *PurchaseService) servePurchase(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Now == nil || s.cfg.Store == nil || s.cfg.Cache == nil || s.cfg.ObservedSource == nil {
		writeProblem(w, http.StatusInternalServerError, "unconfigured", "this exit cannot accept purchases")
		return
	}

	var reqBody PurchaseRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := dec.Decode(&reqBody); err != nil {
		// Distinguishable from a spent note: the buyer must know that a retry
		// of this exact request will never succeed.
		writeProblem(w, http.StatusBadRequest, "malformed", "the request body is not a purchase")
		return
	}

	// The binding address comes from the transport and nowhere else.
	raw, err := s.cfg.ObservedSource(r)
	if err != nil {
		writeProblem(w, http.StatusForbidden, "no-source", "this listener cannot bind the purchase to a peer")
		return
	}
	src, err := netip.ParseAddr(raw)
	if err != nil {
		writeProblem(w, http.StatusForbidden, "no-source", "the peer address is not usable")
		return
	}

	now := s.cfg.Now()
	state, err := LoadState(s.cfg.Store)
	if err != nil && !errors.Is(err, ErrNoState) {
		writeProblem(w, http.StatusInternalServerError, "state", "the admission state cannot be read")
		return
	}

	// The buyer's named window is passed through HONESTLY. Clamping it here
	// would hide Redeem's refusal and hand the buyer a silently narrowed grant
	// they never agreed to, so an over-long request must reach Redeem and be
	// refused there. Only a buyer who names no window gets the challenge's own.
	expires := reqBody.Claim.Challenge.ExpiresAt
	if reqBody.TTLSeconds > 0 {
		expires = now.Add(time.Duration(reqBody.TTLSeconds) * time.Second)
	}
	grantReq := GrantRequest{
		Key:        reqBody.Claim.Buyer,
		Name:       nameFor(reqBody.Claim.Buyer),
		Mode:       reqBody.Claim.Challenge.Mode,
		GrantedAt:  now,
		ExpiresAt:  expires,
		ByteBudget: reqBody.ByteBudget,
	}
	if grantReq.ByteBudget == 0 {
		grantReq.ByteBudget = 1 << 20
	}

	next, err := Redeem(state, now, src, reqBody.Claim, s.cfg.Cache, grantReq)
	if err != nil {
		writeRefusal(w, err)
		return
	}
	file, err := s.persist(next, now)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "state", "the entitlement could not be persisted")
		return
	}
	_ = file

	// Reply in the module's own purchase contract: the app classifies on
	// kind 1022 and reads allotment, metric and device-identifier.
	writeJSON(w, http.StatusOK, map[string]any{
		"kind":              1022,
		"allotment":         int(grantReq.ByteBudget),
		"metric":            string(grantReq.Mode),
		"device-identifier": reqBody.Claim.Buyer.OuterAddress().String(),
		"expires_at":        expires.UTC().Format(time.RFC3339),
	})
}

// persist writes the new state and, from it, the clients file the exit daemon
// actually reads. It goes through Apply rather than rendering a file itself so
// there is exactly one place that decides what the daemon may admit: the same
// function the reconciler uses. Two renderers would be a second declaration of
// the admission fact, which is how this repo's drift incidents started.
//
// Order: state first, file second. A crash between them leaves the state ahead
// of the file, and the next reconciliation pass repairs the file. The reverse
// would leave the daemon admitting a peer the state has never heard of.
func (s *PurchaseService) persist(state State, now time.Time) (ClientsFile, error) {
	applied, err := Apply(state, now, nil)
	if err != nil {
		return ClientsFile{}, err
	}
	blob, err := EncodeState(applied.State)
	if err != nil {
		return ClientsFile{}, err
	}
	if err := s.cfg.Store.WriteAtomic(blob); err != nil {
		return ClientsFile{}, err
	}
	if s.cfg.ClientsStore != nil {
		if err := s.cfg.ClientsStore.WriteAtomic(applied.Rendered); err != nil {
			return ClientsFile{}, err
		}
	}
	return applied.File, nil
}

func nameFor(k PeerKey) string {
	h := k.Hex()
	if len(h) > 12 {
		return "peer-" + h[:12]
	}
	return "peer-" + h
}

func writeRefusal(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNonceReused):
		// A conflict, not a bad request: this purchase already happened.
		writeProblem(w, http.StatusConflict, "replayed", "this purchase was already redeemed")
	case errors.Is(err, ErrClaimExpired):
		writeProblem(w, http.StatusGone, "expired", "the terms this claim accepts have expired; ask for new terms")
	case errors.Is(err, ErrClaimSignature), errors.Is(err, ErrClaimUnbound):
		// One status for both: a caller must not learn whether the key was wrong
		// or the tunnel was, since that is an oracle for probing other peers.
		writeProblem(w, http.StatusForbidden, "not-yours", "this claim does not belong to this peer")
	case errors.Is(err, ErrGrantBeyondChallenge), errors.Is(err, ErrGrantModeMismatch),
		errors.Is(err, ErrGrantKeyMismatch):
		writeProblem(w, http.StatusBadRequest, "terms", err.Error())
	default:
		writeProblem(w, http.StatusBadRequest, "malformed", err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "detail": detail})
}
