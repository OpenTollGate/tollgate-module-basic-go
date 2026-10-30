package main

// HTTP channel surface for the Spilman rail — the payee half of "a router can be
// paid over a channel".
//
// # Why this file exists
//
// The Go rail had a correct, verified ledger and NO WAY TO BE PAID. `grep -rn
// spilman src/http/` found nothing, and the rail was not imported anywhere in the
// module, so a Go router could not participate in a purchase at all. Verification
// without a surface is necessary and not sufficient; this closes the second half.
//
// # The shape, and why it mirrors the Rust module
//
// The Rust module's routes are already proven on hardware (14/14 on a physical
// router, including a measured "zero mint calls between open and settle"), so the
// wire contract here is deliberately the same:
//
//	GET  /spilman/params    -> what this payee will accept
//	POST /spilman/channel   -> register an opened channel
//	POST /spilman/payment   -> a signed balance update
//	GET  /spilman/status    -> the credit decision and the balance behind it
//
// Two modules that disagree about a status code are two modules that cannot
// interoperate, so the codes are chosen to match rather than invented:
//
//	404  the rail is disabled, or the channel is unknown
//	409  the request was well-formed and the answer is NO (a refused payment)
//	500  WE failed (an unreadable ledger)
//
// The 409/500 split matters. A refused payment is not a server fault, and an
// unreadable ledger is not "unknown channel" — reporting either as the other
// blames the caller for our failure, or hides ours behind theirs.
//
// # Mounting
//
// Routes exist ONLY when the rail is enabled. With it off, every /spilman path
// 404s because nothing is registered for it — absence, which is stronger than a
// handler that abstains. See spilmanEnabled().

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/spilman"
)

// spilmanRail is the process-wide payee rail, built lazily on first use.
//
// `nil` means the rail is DISABLED. It is never "enabled but unauthenticated":
// the ledger is opened with this build's validator, and in a default
// (CGO_ENABLED=0) build that validator refuses every payment, so an enabled rail
// still cannot credit an unverified balance.
var (
	spilmanMu   sync.Mutex
	spilmanOnce *spilman.SellerRail
	spilmanErr  error
)

// spilmanConfig returns the Spilman config block, or a zero value when no config
// manager exists yet.
//
// A zero value is OFF, which is the safe answer during boot: the routes are mounted
// from `registerAPIHandlers`, and if config is somehow unavailable at that point the
// correct behaviour is to expose no payment surface rather than to guess.
func spilmanConfig() config_manager.SpilmanConfig {
	if configManager == nil {
		return config_manager.SpilmanConfig{}
	}
	cfg := configManager.GetConfig()
	if cfg == nil {
		return config_manager.SpilmanConfig{}
	}
	return cfg.Spilman
}

// spilmanEnabled reports whether the Spilman rail should be mounted.
//
// Absent or `enabled: false` means OFF, and OFF is the default: a router that has
// not opted in must not expose a payment surface at all.
func spilmanEnabled() bool {
	return spilmanConfig().Enabled
}

// getSpilmanRail returns the payee rail, or nil when disabled.
//
// Built once. A construction failure is returned so the caller can answer 500
// rather than pretending the rail is absent — "we could not open our ledger" and
// "this router does not do channels" are different facts.
func getSpilmanRail() (*spilman.SellerRail, error) {
	spilmanMu.Lock()
	defer spilmanMu.Unlock()
	if spilmanOnce != nil || spilmanErr != nil {
		return spilmanOnce, spilmanErr
	}
	cfg := spilmanConfig()
	if !cfg.Enabled {
		return nil, nil
	}
	path := strings.TrimSpace(cfg.ChannelDBPath)
	if path == "" {
		// No durable ledger means no durable credit. Refusing to start the rail is
		// the honest answer: a payee that forgets what it was paid will serve bytes
		// it cannot collect for.
		return nil, errSpilmanNoDB
	}
	ledger, err := spilman.OpenLedger(path)
	if err != nil {
		spilmanErr = err
		return nil, err
	}
	spilmanOnce = spilman.NewSellerRail(ledger)
	return spilmanOnce, nil
}

type spilmanError string

func (e spilmanError) Error() string { return string(e) }

const errSpilmanNoDB = spilmanError(
	"spilman is enabled but spilman.channel_db_path is unset; refusing to " +
		"run a payee with no durable channel ledger")

func writeSpilmanJSON(w http.ResponseWriter, code int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// railOrRefuse resolves the rail, writing the correct response when it is
// unavailable. Returns nil when the caller must stop.
func railOrRefuse(w http.ResponseWriter) *spilman.SellerRail {
	if !spilmanEnabled() {
		// Disabled: the routes are not registered, so this is unreachable in
		// practice. Kept as a belt-and-braces refusal rather than a panic.
		writeSpilmanJSON(w, http.StatusNotFound, map[string]any{
			"status": 0, "error": "spilman rail is not enabled"})
		return nil
	}
	rail, err := getSpilmanRail()
	if err != nil {
		mainLogger.WithError(err).Error("spilman rail unavailable")
		writeSpilmanJSON(w, http.StatusInternalServerError, map[string]any{
			"status": 0, "error": "spilman rail unavailable"})
		return nil
	}
	if rail == nil {
		writeSpilmanJSON(w, http.StatusNotFound, map[string]any{
			"status": 0, "error": "spilman rail is not enabled"})
		return nil
	}
	return rail
}

// ---------------------------------------------------------------------------
// GET /spilman/params
// ---------------------------------------------------------------------------

// handleSpilmanParams advertises what this payee will accept.
//
// A payer needs the unit and the rate BEFORE it opens anything: the rate decides
// how many bytes a sat buys, and a mismatch here is a channel that both sides
// price differently. `validator` is included so an operator can see, from the
// wire, whether this build can authenticate at all — in a default build it reads
// "not-compiled" and every payment will be refused, which is worth knowing before
// funding a channel rather than after.
//
// # receiver_pubkey
//
// The key a payer MUST name as the channel's `receiver_pubkey`. Without it a payer
// can only open against a key of its own choosing — and this payee signs every close
// with its own, so such a channel funds at the mint and can never be settled. That
// was a real, silent money loss, not a hypothetical: the field was absent here while
// the registration handler accepted whatever the caller sent.
//
// It is `null` when the payee has no merchant identity to derive from. Advertising a
// key we cannot sign with would be worse than advertising none.
func handleSpilmanParams(w http.ResponseWriter, r *http.Request) {
	rail := railOrRefuse(w)
	if rail == nil {
		return
	}
	receiver := spilmanReceiverPubkeyHex()
	body := map[string]any{
		"status":                1,
		"unit":                  spilman.UnitSat,
		"in_bytes_per_millisat": spilman.TollgateParityInBytesPerMillisat,
		"validator":             rail.Validator(),
		"receiver_pubkey":       nil,
	}
	if receiver != "" {
		body["receiver_pubkey"] = receiver
	}
	writeSpilmanJSON(w, http.StatusOK, body)
}

// ---------------------------------------------------------------------------
// POST /spilman/channel
// ---------------------------------------------------------------------------

// spilmanChannelRequest is the wire shape of a channel registration.
//
// The funding record fields are NOT optional bookkeeping: they are the only thing
// a later payment signature can be checked against. A registration without them
// is refused by the rail rather than stored, because a stored row that can never
// verify is a durable denial of service on that channel id.
type spilmanChannelRequest struct {
	ChannelID         string `json:"channel_id"`
	Unit              string `json:"unit"`
	Capacity          uint64 `json:"capacity"`
	Expiry            uint64 `json:"expiry"`
	ReceiverPubkey    string `json:"receiver_pubkey"`
	MintURL           string `json:"mint_url"`
	KeysetID          string `json:"keyset_id"`
	FundingTxID       string `json:"funding_tx_id"`
	ParamsJSON        string `json:"params_json"`
	FundingProofsJSON string `json:"funding_proofs_json"`
	ChannelSecretHex  string `json:"channel_secret_hex"`
	KeysetInfoJSON    string `json:"keyset_info_json"`
}

func handleSpilmanChannel(w http.ResponseWriter, r *http.Request) {
	rail := railOrRefuse(w)
	if rail == nil {
		return
	}
	var req spilmanChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSpilmanJSON(w, http.StatusBadRequest, map[string]any{
			"status": 0, "error": "malformed channel request"})
		return
	}
	if strings.TrimSpace(req.Unit) == "" {
		req.Unit = spilman.UnitSat
	}
	// REFUSE A CHANNEL WE COULD NEVER SETTLE.
	//
	// This happens BEFORE rail.Open, so nothing is written: the payer's funding is
	// already committed at the mint by the time this body arrives, and a 200 here
	// would take responsibility for a channel only some other key can close. The
	// answer is a 400 that names the field, not a 200 that hides the problem until
	// the close silently fails.
	if err := requireOwnReceiverKey(req.ReceiverPubkey); err != nil {
		writeSpilmanJSON(w, http.StatusBadRequest, map[string]any{
			"status": 0, "error": err.Error()})
		return
	}
	status, err := rail.Open(r.Context(), spilman.ChannelOpenRequest{
		ChannelID:         req.ChannelID,
		Unit:              req.Unit,
		Capacity:          req.Capacity,
		Expiry:            req.Expiry,
		ReceiverPubkey:    req.ReceiverPubkey,
		MintURL:           req.MintURL,
		KeysetID:          req.KeysetID,
		FundingTxID:       req.FundingTxID,
		ParamsJSON:        req.ParamsJSON,
		FundingProofsJSON: req.FundingProofsJSON,
		ChannelSecretHex:  req.ChannelSecretHex,
		KeysetInfoJSON:    req.KeysetInfoJSON,
	})
	if err != nil {
		// A malformed or conflicting registration is the CALLER's problem, and it
		// is not a payment refusal, so it is 400 rather than 409.
		writeSpilmanJSON(w, http.StatusBadRequest, map[string]any{
			"status": 0, "error": err.Error()})
		return
	}
	writeSpilmanJSON(w, http.StatusOK, map[string]any{
		"status":     1,
		"channel_id": status.ChannelID,
		"state":      string(status.State),
		"capacity":   status.Capacity,
	})
}

// ---------------------------------------------------------------------------
// POST /spilman/payment
// ---------------------------------------------------------------------------

type spilmanPaymentRequest struct {
	ChannelID string `json:"channel_id"`
	Balance   uint64 `json:"balance"`
	Signature string `json:"signature"`
}

// handleSpilmanPayment records a signed balance update.
//
// THE ORDERING LIVES IN THE RAIL, NOT HERE. `AcceptPayment` reaches
// `ChannelLedger.RecordPayment`, which authenticates the signature against the
// persisted funding record and only then does the durable CAS write. This handler
// translates and maps errors; it must never write a balance itself, or it would
// become a second, unauthenticated path into the ledger.
func handleSpilmanPayment(w http.ResponseWriter, r *http.Request) {
	rail := railOrRefuse(w)
	if rail == nil {
		return
	}
	var req spilmanPaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSpilmanJSON(w, http.StatusBadRequest, map[string]any{
			"status": 0, "error": "malformed payment request"})
		return
	}
	outcome, err := rail.AcceptPayment(r.Context(), spilman.ChannelPayment{
		ChannelID: req.ChannelID,
		Balance:   req.Balance,
		Signature: req.Signature,
	})
	if err != nil {
		// The request was well-formed and the answer is "no". That is 409, not 500:
		// nothing is broken here, and the payer should not retry the same payment.
		mainLogger.WithFields(map[string]any{
			"channel_id": req.ChannelID,
			"balance":    req.Balance,
			"error":      err.Error(),
		}).Warn("spilman payment refused")
		writeSpilmanJSON(w, http.StatusConflict, map[string]any{
			"status": 0, "error": err.Error()})
		return
	}
	writeSpilmanJSON(w, http.StatusOK, map[string]any{
		"status":  1,
		"outcome": string(outcome),
	})
}

// ---------------------------------------------------------------------------
// GET /spilman/status
// ---------------------------------------------------------------------------

// handleSpilmanStatus reports whether a channel is known, and the balance behind
// the credit decision.
//
// `balance` is null for a channel that is registered but has never been paid —
// deliberately distinct from 0. Collapsing the two would tell a payer "you have
// paid nothing" when the truth is "I have no record of a payment yet", and those
// want different actions from the payer.
func handleSpilmanStatus(w http.ResponseWriter, r *http.Request) {
	rail := railOrRefuse(w)
	if rail == nil {
		return
	}
	channelID := strings.TrimSpace(r.URL.Query().Get("channel_id"))
	if channelID == "" {
		writeSpilmanJSON(w, http.StatusBadRequest, map[string]any{
			"status": 0, "error": "channel_id is required"})
		return
	}
	status, err := rail.Status(r.Context(), channelID)
	if err != nil {
		// Unknown vs unreadable. The rail distinguishes them; the wire must too,
		// because "I do not know this channel" is actionable by the payer and
		// "my ledger is unreadable" is not.
		if isSpilmanUnknownChannel(err) {
			writeSpilmanJSON(w, http.StatusNotFound, map[string]any{
				"status": 0, "error": "unknown channel", "channel_id": channelID})
			return
		}
		mainLogger.WithError(err).WithField("channel_id", channelID).
			Error("spilman status read failed")
		writeSpilmanJSON(w, http.StatusInternalServerError, map[string]any{
			"status": 0, "error": "channel ledger unreadable"})
		return
	}
	body := map[string]any{
		"status":     1,
		"channel_id": status.ChannelID,
		"state":      string(status.State),
		"unit":       status.Unit,
		"capacity":   status.Capacity,
	}
	// A never-paid channel has no balance row. Report null, not 0.
	if status.PaymentCount == 0 {
		body["balance"] = nil
	} else {
		body["balance"] = status.Balance
	}
	writeSpilmanJSON(w, http.StatusOK, body)
}

// isSpilmanUnknownChannel reports whether an error means "no such channel".
//
// Kept narrow on purpose: treating every error as unknown would tell a payer its
// funded channel does not exist when our ledger is simply unreadable.
func isSpilmanUnknownChannel(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "unknown channel")
}

// ---------------------------------------------------------------------------
// POST /spilman/access
// ---------------------------------------------------------------------------

type spilmanAccessRequest struct {
	Mac       string `json:"mac"`
	ChannelID string `json:"channel_id"`
}

// handleSpilmanAccess binds a client MAC to a channel.
//
// # Why this route exists
//
// Without it `session.ChannelID` has no production writer, so the access path is
// correct and unreachable — the exact defect a cold review found on the Rust side.
// This is the Go equivalent of `POST /spilman/access`.
//
// # What it does NOT do
//
// It grants nothing and opens no gate. It records WHICH CHANNEL PAYS for this client;
// whether the client is served is decided by the 2-second sweep, which recomputes the
// effective ceiling from the channel's VERIFIED balance. A binding to a channel with
// no credit buys nothing, which is correct: the binding is a pointer, not a payment.
func handleSpilmanAccess(w http.ResponseWriter, r *http.Request) {
	rail := railOrRefuse(w)
	if rail == nil {
		return
	}
	var req spilmanAccessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSpilmanJSON(w, http.StatusBadRequest, map[string]any{
			"status": 0, "error": "malformed access request"})
		return
	}
	if strings.TrimSpace(req.Mac) == "" || strings.TrimSpace(req.ChannelID) == "" {
		writeSpilmanJSON(w, http.StatusBadRequest, map[string]any{
			"status": 0, "error": "mac and channel_id are required"})
		return
	}
	// The channel must already be registered. Binding to a typo would create a
	// session that can never be credited, with a refusal indistinguishable from a bug.
	if _, err := rail.Status(r.Context(), req.ChannelID); err != nil {
		if isSpilmanUnknownChannel(err) {
			writeSpilmanJSON(w, http.StatusNotFound, map[string]any{
				"status": 0, "error": "unknown channel", "channel_id": req.ChannelID})
			return
		}
		writeSpilmanJSON(w, http.StatusInternalServerError, map[string]any{
			"status": 0, "error": "channel ledger unreadable"})
		return
	}
	m := merchantProvider.inner.GetMerchant()
	if m == nil {
		writeSpilmanJSON(w, http.StatusNotFound, map[string]any{
			"status": 0, "error": "merchant not ready"})
		return
	}
	effective, ok := m.BindChannel(req.Mac, req.ChannelID)
	if !ok {
		// A binding attaches to an EXISTING session. If it created one, anyone able
		// to reach this route could conjure access.
		writeSpilmanJSON(w, http.StatusNotFound, map[string]any{
			"status": 0, "error": "no live session for this mac; a binding attaches " +
				"to an existing session and does not create access"})
		return
	}
	writeSpilmanJSON(w, http.StatusOK, map[string]any{
		"status":              1,
		"mac":                 req.Mac,
		"channel_id":          req.ChannelID,
		"effective_allotment": effective,
	})
}

// ---------------------------------------------------------------------------
// Mounting
// ---------------------------------------------------------------------------

// registerSpilmanRoutes mounts the channel surface, and ONLY when the rail is on.
//
// # Why "not registered" was NOT enough for 404
//
// The first version of this function simply returned when the rail was disabled, on
// the reasoning that an unregistered path 404s. It does not. This mux has a `"/"`
// catch-all (`registerAPIHandlers` -> `HandleRoot` -> `handleDetails`), which matches
// EVERY unmatched path, so a disabled `/spilman/payment` answered **200** and the
// test caught it:
//
//	disabled: /spilman/params = 200, want 404
//
// Answering 200 on a payment endpoint for a router that never enabled payments is
// worse than a wrong status code — it tells a payer the surface exists. So the
// disabled case now registers an EXPLICIT 404 over the whole `/spilman/` subtree,
// which the catch-all cannot override.
// installSpilmanRailOnMerchant hands the rail to the merchant so the 2-second sweep
// can recompute a session's effective ceiling from the channel's verified balance.
//
// Called after the merchant is constructed. Harmless when the rail is off: it clears
// whatever was there, and a nil rail makes refreshEffectiveAllotment the identity.
func installSpilmanRailOnMerchant() {
	rail, err := getSpilmanRail()
	if err != nil {
		mainLogger.WithError(err).Error("spilman rail unavailable; access path will not draw on channels")
		return
	}
	if rail == nil {
		return
	}
	if m := merchantProvider.inner.GetMerchant(); m != nil {
		if setter, ok := m.(interface {
			SetChannelRail(rail *spilman.SellerRail)
		}); ok {
			setter.SetChannelRail(rail)
			mainLogger.Info("spilman rail installed on the merchant access path")
		}
	}
}

func registerSpilmanRoutes(mux *http.ServeMux) {
	if !spilmanEnabled() {
		mainLogger.Debug("spilman rail disabled; /spilman/* pinned to 404")
		// The trailing slash registers the SUBTREE, so every /spilman/... path
		// reaches this handler instead of the catch-all.
		mux.HandleFunc("/spilman/", func(w http.ResponseWriter, r *http.Request) {
			writeSpilmanJSON(w, http.StatusNotFound, map[string]any{
				"status": 0, "error": "spilman rail is not enabled"})
		})
		return
	}
	mainLogger.Info("spilman channel surface mounted at /spilman/*")
	mux.HandleFunc("/spilman/params", CorsMiddleware(handleSpilmanParams))
	mux.HandleFunc("/spilman/channel", CorsMiddleware(handleSpilmanChannel))
	mux.HandleFunc("/spilman/payment", CorsMiddleware(handleSpilmanPayment))
	mux.HandleFunc("/spilman/status", CorsMiddleware(handleSpilmanStatus))
	// The ONE production writer of session.ChannelID. Without it the access path is
	// wired and unreachable.
	mux.HandleFunc("/spilman/access", CorsMiddleware(handleSpilmanAccess))
}

// compile-time guard: the rail must satisfy what this file assumes of it.
var _ = func() context.Context { return context.Background() }
