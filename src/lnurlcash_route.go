package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant/lnurlcash"
	log "github.com/sirupsen/logrus"
)

// The LNURLcash payment route.
//
// Three things distinguish it from POST / (the Cashu path), and they are the
// whole of the integration:
//
//  1. The body is a NOTE, not a token. It is redeemed against the note's issuer
//     rather than received into the wallet, so the grant cannot happen
//     synchronously with the issuer's reply.
//  2. The grant condition is still "our mint confirmed the money". The redeemer
//     asks OUR settlement mint whether the quote it invoiced has been paid. The
//     issuer's "OK" is not value; it is permission to ask.
//  3. Nothing about it exists while the operator has not enabled it: the route
//     is not registered (404), never registered-and-refusing (403).
//
// The customer may be offline — they hand over a note string. The GATE must be
// online, because it performs the redemption.

// maxLNURLCashBodyBytes bounds the note. A bech32 LNURL or an https URL plus
// whitespace is far under this; the cap makes an oversized body a 413 before it
// is parsed.
const maxLNURLCashBodyBytes = 4 << 10

// lnurlCashRedeemer is the process-wide redeemer. Nil while the feature is
// disabled, which is also what makes the route absent.
var (
	lnurlCashMu       sync.RWMutex
	lnurlCashRedeemer *lnurlcash.Redeemer
)

// initLNURLCash wires the payment method if the operator enabled it.
//
// It is deliberately not fatal: a router whose operator turned this on but whose
// journal cannot be opened must keep serving the Cashu path — the thing that was
// working before — rather than fail to boot. The method is then simply
// unavailable, and that is logged at error level so it is not silent.
func initLNURLCash() {
	if configManager == nil {
		setLNURLCashRedeemer(nil)
		return
	}
	cfg := configManager.GetConfig()
	if cfg == nil || !cfg.LNURLCash.Enabled {
		setLNURLCashRedeemer(nil)
		return
	}

	redeemerCfg := merchant.LNURLCashConfigFrom(cfg.LNURLCash)

	// The journal lives beside the wallet, next to quotes.json: same filesystem,
	// same durability assumptions, same backup surface.
	store, err := lnurlcash.OpenIntentStore(
		filepath.Join(merchant.WalletDataDir(configManager), "lnurlcash-intents.json"))
	if err != nil {
		mainLogger.WithError(err).Error("LNURLcash is enabled but its redemption journal could not be opened — the payment method is unavailable")
		setLNURLCashRedeemer(nil)
		return
	}

	gate := merchant.NewLNURLCashGateLive(
		func() *merchant.Merchant {
			// The merchant is swapped at runtime when the reachable mint set
			// changes; resolving it per call keeps settlement on the live one.
			if m, ok := merchantProvider.inner.GetMerchant().(*merchant.Merchant); ok {
				return m
			}
			return nil
		},
		func() []config_manager.MintConfig {
			if configManager == nil || configManager.GetConfig() == nil {
				return nil
			}
			return configManager.GetConfig().AcceptedMints
		},
	)

	timeout := time.Duration(redeemerCfg.RedemptionTimeoutSeconds) * time.Second

	// A lab-only escape hatch: the cloud-lab lane's fake issuer is a container
	// reachable only over plain http on the lab bridge, which the production
	// guards correctly refuse. It is environment-only (never a config-file field,
	// so an operator cannot enable it by editing JSON) and it is announced.
	plaintextIssuers := os.Getenv("TOLLGATE_ALLOW_PLAINTEXT_ISSUERS") == "1"
	if plaintextIssuers {
		mainLogger.Warn("LNURLcash: TOLLGATE_ALLOW_PLAINTEXT_ISSUERS=1 — plain-http and private-address issuers are ACCEPTED. This is for a lab only; never run it on a router handling real payments.")
	}
	client := lnurlcash.NewClient(lnurlcash.ClientOptions{
		Timeout:                  timeout,
		PlainTextIssuersForTests: plaintextIssuers,
	})

	setLNURLCashRedeemer(lnurlcash.NewRedeemer(redeemerCfg, gate, store, client))

	// Retention is a promise the schema makes ("0 keeps everything"), so something has
	// to actually enforce it: Prune existed but nothing called it, and the journal grew
	// for the life of the install while the operator's config said otherwise. Prune
	// never drops a pending/submitted record, so a sweep cannot reopen a note.
	go lnurlcashPruneLoop(store, redeemerCfg.RetentionSeconds)

	mainLogger.WithFields(log.Fields{
		"max_note_sats": redeemerCfg.MaxNoteSats,
		"timeout_s":     redeemerCfg.RedemptionTimeoutSeconds,
		"retention_s":   redeemerCfg.RetentionSeconds,
	}).Info("LNURLcash payment method ENABLED (opt-in); issuer custody is accepted between presentation and redemption")
}

// lnurlcashPruneLoop drops terminal redemption records past their retention, hourly.
//
// Hourly rather than on a timer per record: the journal is small, the sweep is a file
// rewrite, and a coarse schedule cannot be the reason a payment path misbehaves. A
// retention of 0 means "keep everything" and is honoured by not starting the loop's
// work at all — Prune(0) is a no-op, but skipping the ticker is clearer in a log.
func lnurlcashPruneLoop(store *lnurlcash.IntentStore, retentionSeconds int) {
	if retentionSeconds <= 0 {
		mainLogger.Info("LNURLcash: retention_seconds=0 — finished redemption records are kept indefinitely")
		return
	}
	retention := time.Duration(retentionSeconds) * time.Second
	// One sweep shortly after boot so a long-running install converges without waiting
	// an hour, then hourly.
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		<-timer.C
		if dropped, err := store.Prune(retention); err != nil {
			mainLogger.WithError(err).Warn("LNURLcash: could not prune finished redemption records; the journal will grow until this succeeds")
		} else if dropped > 0 {
			mainLogger.WithField("dropped", dropped).Info("LNURLcash: pruned finished redemption records past retention")
		}
		timer.Reset(time.Hour)
	}
}

func setLNURLCashRedeemer(r *lnurlcash.Redeemer) {
	lnurlCashMu.Lock()
	defer lnurlCashMu.Unlock()
	lnurlCashRedeemer = r
}

func getLNURLCashRedeemer() *lnurlcash.Redeemer {
	lnurlCashMu.RLock()
	defer lnurlCashMu.RUnlock()
	return lnurlCashRedeemer
}

// registerLNURLCashRoute registers the endpoint ONLY when the method is live.
// Registering it unconditionally and refusing inside would advertise a payment
// method this gate will not honour.
func registerLNURLCashRoute(mux *http.ServeMux) {
	initLNURLCash()
	if getLNURLCashRedeemer() == nil {
		return
	}
	mux.HandleFunc("/lnurlcash/pay", func(w http.ResponseWriter, r *http.Request) {
		mainLogger.WithField("remote_addr", r.RemoteAddr).Debug("Hit /lnurlcash/pay endpoint")
		RateLimitMiddleware(CorsMiddleware(requireStarted(HandleLNURLCashPay)))(w, r)
	})
}

// HandleLNURLCashPay accepts a note and reports the redemption outcome.
func HandleLNURLCashPay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	redeemer := getLNURLCashRedeemer()
	if redeemer == nil {
		// The route should not be registered in this state; answer as absent so
		// the two paths cannot disagree.
		http.NotFound(w, r)
		return
	}

	// Same identity contract as every other client-scoped route: the client is
	// the one at the far end of the socket. A `mac` claim is not consulted, for
	// the same reason POST / does not consult it — this call decides which
	// device is granted access, and the note is consumed before the grant.
	macAddress, err := clientIdentity(w, r, "")
	if err != nil {
		mainLogger.WithError(err).WithField("remote_addr", r.RemoteAddr).
			Warn("LNURLcash payment refused: the client has no resolvable identity")
		sendNoticeResponse(w, merchantProvider.inner.GetMerchant(), http.StatusBadRequest, "error", errDeviceUnresolvedCode,
			deviceUnresolvedMessage, "")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLNURLCashBodyBytes))
	if err != nil {
		mainLogger.WithError(err).Warn("Rejected oversized LNURLcash request body")
		sendNoticeResponse(w, merchantProvider.inner.GetMerchant(), http.StatusRequestEntityTooLarge, "error", "invalid-request",
			"Invalid request", "")
		return
	}

	// The body IS a bearer secret. Log a fingerprint only — the same treatment
	// POST / gives a token, for the same reason: debug is the level an operator
	// enables exactly when a payment misbehaves, i.e. when the log gets shared.
	fp := redeemer.FingerprintOfPresented(string(body))
	mainLogger.WithFields(log.Fields{
		"body_len":  len(body),
		"note_fp":   fp,
		"remote":    r.RemoteAddr,
		"client_id": macAddress,
	}).Debug("Received LNURLcash note")

	result, err := redeemer.Redeem(r.Context(), string(body), macAddress)
	if err != nil {
		mainLogger.WithError(err).WithField("note_fp", fp).Info("LNURLcash redemption did not complete")
	}

	writeLNURLCashResponse(w, lnurlCashResponse{
		State:           result.State,
		Granted:         result.Granted,
		AccessGranted:   result.Granted,
		Quote:           result.QuoteID,
		Invoice:         result.Invoice,
		MintURL:         result.MintURL,
		Amount:          result.AmountSats,
		NoteFingerprint: fp,
		Message:         result.Message,
	})
}

// lnurlCashResponse is the customer-facing answer.
//
// It carries the quote so the portal's existing `GET /ln-invoice?quote=` poll
// can watch settlement with no portal change, and the state string so a portal
// can distinguish "you were not charged" from "do not present this note again"
// — the two answers that matter to someone holding a bearer secret.
type lnurlCashResponse struct {
	State           string `json:"state"`
	Granted         bool   `json:"granted"`
	AccessGranted   bool   `json:"access_granted"`
	Quote           string `json:"quote,omitempty"`
	Invoice         string `json:"invoice,omitempty"`
	MintURL         string `json:"mint_url,omitempty"`
	Amount          uint64 `json:"amount,omitempty"`
	NoteFingerprint string `json:"note_fingerprint,omitempty"`
	Message         string `json:"message,omitempty"`
	Error           string `json:"error,omitempty"`
}

func writeLNURLCashResponse(w http.ResponseWriter, resp lnurlCashResponse) {
	w.Header().Set("Content-Type", "application/json")
	// The default is NOT 200. A Result built by an error path carries no state, and
	// answering 200 with an empty state told a portal "paid" for a redemption that
	// never happened. An unclassified outcome is a refusal until proven otherwise.
	status := http.StatusPaymentRequired
	switch resp.State {
	case lnurlcash.StateUnavailable, lnurlcash.StateAbandoned:
		// The customer was not charged. 402 is the honest code: payment was
		// required and did not happen.
		status = http.StatusPaymentRequired
	case lnurlcash.StateAmbiguous:
		// The issuer's answer was lost. 409 — the request cannot be safely
		// repeated, which is exactly what the customer needs to know.
		status = http.StatusConflict
	case lnurlcash.StatePending:
		status = http.StatusAccepted
	case lnurlcash.StateRedeemed:
		// 200 whether or not the grant has landed yet: the note has settled or is
		// settling, and the quote poll carries the rest.
		status = http.StatusOK
	}
	if resp.Message != "" && (status == http.StatusPaymentRequired || status == http.StatusConflict) {
		resp.Error = resp.Message
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}
