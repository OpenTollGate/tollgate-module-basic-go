package main

import (
	"errors"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/nbd-wtf/go-nostr"
	"github.com/sirupsen/logrus"
)

// The startup gate, and the "starting" merchant the money path answers with
// while the real one is still being constructed.
//
// WHY THIS FILE EXISTS (t_54359164, measured with the shipped binary on a host):
// on a cold boot the payment API :2121 did not accept a connection for 2-9
// minutes, while `/etc/init.d/tollgate-wrt status` reported `running` the whole
// time — procd reports the PROCESS, and the process was alive. Two network
// stages ran before http.ListenAndServe() was ever reached:
//
//	1. the startup mint probe (one mint at a time, 30 s each) — bounded to
//	   defaultStartupProbeBudget by 65ed848d;
//	2. wallet construction through gonuts, which has no http.Client{Timeout:}
//	   anywhere in wallet/ and is therefore unbounded, not merely slow — and it
//	   runs once per remaining accepted mint (tollwallet.go registerMint).
//
// Measured time from exec to :2121 accepting, 7 accepted mints, 1 s sampling:
// 347.1 s with the mints blackholed (accept the connection, never answer) and
// 113.1 s with them refused. Bounding stage 1 was not enough: in the refused
// shape the two binaries are indistinguishable, because the whole cost there is
// stage 2.
//
// The fix is ordering, not a shorter timeout. The listener and the CLI socket
// come up before any mint-dependent work, and every mint-dependent request is
// answered with an explicit "starting" refusal until the merchant has been
// constructed. Money-path semantics are deliberately untouched: which mints are
// probed, what a probe result means, which mints are advertised, and the
// degraded -> full upgrade path are all exactly what they were — only the order
// of "serve" and "construct" changed.

// codeStarting is the machine-readable code of the refusal a mint-dependent
// request gets while the merchant is still being constructed. It is additive
// next to the `status`/`error` pair every portal already parses, the same shape
// as errDeviceUnresolvedCode and codeQuoteRateLimited.
const codeStarting = "starting"

// startingRetryAfterSeconds is what the refusal tells a caller to wait. The
// wallet stage is dominated by mint calls that time out at ~30 s each, so a few
// seconds is an honest poll cadence: quick enough that a customer's page shows
// the sale the moment it is available, slow enough not to add a request storm
// to a router that is still coming up.
const startingRetryAfterSeconds = 5

// startingMessage is what a customer-facing caller is told while the money path
// is up but the merchant is not. It is actionable (retry) and it is true.
const startingMessage = "This TollGate is starting up: the payment API is up, but the wallet and the mint health are still loading. Please retry in a few seconds."

// errMerchantStarting is what the placeholder merchant answers every wallet-ish
// question with while the real merchant is being constructed. It is a distinct
// error rather than a zero value because a zero value is a *wrong answer*:
// "balance 0" and "usage -1/-1" would tell a paying customer and the operator
// that there is no money, when the truth is that the wallet is not loaded yet.
var errMerchantStarting = errors.New("tollgate is still starting: the wallet and the mint health are not loaded yet")

// startupGate is the single question every mint-dependent request asks before
// it touches the merchant: has construction finished? It is read on every such
// request, so the read is a single atomic load and the write happens once.
type startupGate struct {
	ready atomic.Bool

	mu    sync.Mutex
	stage string
}

// stageBindingAPI and stageConnectingMerchant are the two stages the gate can
// be in before it opens. They are named in the log line a refusal emits, so an
// operator reading `logread` during a boot can tell "the wallet is loading"
// from "the merchant is up".
const (
	stageBindingAPI         = "binding the payment API"
	stageConnectingMerchant = "initializing the merchant (mint probes + wallet)"
)

func newStartupGate() *startupGate {
	return &startupGate{stage: stageBindingAPI}
}

// apiStartup is the process-wide gate. It is closed when the process starts and
// is opened at the end of the boot sequence (init()), i.e. after the merchant
// has been installed behind it.
var apiStartup = newStartupGate()

func (g *startupGate) isReady() bool { return g.ready.Load() }

func (g *startupGate) setStage(stage string) {
	g.mu.Lock()
	g.stage = stage
	g.mu.Unlock()
}

func (g *startupGate) currentStage() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stage
}

// markReady opens the gate. Called once, after the constructed merchant has
// been installed behind merchantProvider.
func (g *startupGate) markReady() {
	g.setStage("ready")
	g.ready.Store(true)
}

// startingRefusals keeps the "still starting" log line to one per client key per
// window (same bounded, LRU-evicted throttle as quoteRefusals): the portal polls
// while it waits, and a log line per refused poll is the flood the router's
// `logread` cannot afford during exactly the incident this line describes.
var startingRefusals quoteRefusalLog

// requireStarted wraps the mint-dependent handlers. While construction is in
// flight it answers an explicit "starting" refusal instead of letting a handler
// touch a merchant that does not exist yet — and instead of the refused
// connection a caller used to get, which told nobody anything. It is installed
// inside CorsMiddleware so a browser fetch from the portal can still read the
// refusal (the OPTIONS preflight never reaches it).
func requireStarted(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if apiStartup.isReady() {
			next(w, r)
			return
		}

		key := clientLimiterKey(r)
		if shouldLog, total := startingRefusals.allow(key); shouldLog {
			mainLogger.WithFields(logrus.Fields{
				"client": key,
				"stage":  apiStartup.currentStage(),
				"total":  total,
			}).Warn("Money path is up but the merchant is still initializing: refused with an explicit starting state")
		}

		writeLightningRefusal(w, http.StatusServiceUnavailable, codeStarting,
			startingMessage, startingRetryAfterSeconds)
	}
}

// startingMerchant is what every consumer of merchantProvider sees between "the
// money path is up" (the listener and the CLI socket are bound) and
// "construction finished" (the real merchant is installed). It exists so that
// the provider handed to the CLI server — and to anything else that reads it
// before construction completes — is never nil: a nil MerchantInterface is a
// panic in whichever goroutine reads it first, which would take the whole
// daemon down, where this answers "still starting" and nothing else.
//
// It is never installed on top of a real merchant: installMerchant replaces it
// with the merchant merchant.New() returned.
type startingMerchant struct{}

func (startingMerchant) CreatePaymentToken(string, uint64) (string, error) {
	return "", errMerchantStarting
}

func (startingMerchant) CreatePaymentTokenWithOverpayment(string, uint64, uint64, uint64) (string, error) {
	return "", errMerchantStarting
}

func (startingMerchant) DrainMint(string) (string, uint64, error) {
	return "", 0, errMerchantStarting
}

func (startingMerchant) RequestLightningInvoice(string, string, uint64) (*merchant.LightningInvoice, error) {
	return nil, errMerchantStarting
}

func (startingMerchant) GetLightningInvoiceStatus(string, string) (*merchant.LightningQuoteStatus, error) {
	return nil, errMerchantStarting
}

// GetAcceptedMints answers an empty set rather than an error: it is what the
// advertisement is built from, and every route that serves it is gated.
func (startingMerchant) GetAcceptedMints() []config_manager.MintConfig { return nil }

func (startingMerchant) GetBalance() uint64 { return 0 }

func (startingMerchant) GetBalanceByMint(string) uint64 { return 0 }

func (startingMerchant) GetAllMintBalances() map[string]uint64 { return nil }

func (startingMerchant) PurchaseSession(string, string) (*nostr.Event, error) {
	return nil, errMerchantStarting
}

// GetAdvertisement is empty while starting: the routes that serve it are gated,
// and an advertisement built from a wallet that is not loaded would advertise
// the wrong thing.
func (startingMerchant) GetAdvertisement() string { return "" }

// StartPayoutRoutine and StartDataUsageMonitoring are no-ops: the real
// merchant starts them (this placeholder is never the merchant the process
// runs on, and installMerchant() replaces it).
func (startingMerchant) StartPayoutRoutine() {}

func (startingMerchant) StartDataUsageMonitoring() {}

func (startingMerchant) CreateNoticeEvent(string, string, string, string) (*nostr.Event, error) {
	return nil, errMerchantStarting
}

func (startingMerchant) GetSession(string) (*merchant.CustomerSession, error) {
	return nil, errMerchantStarting
}

func (startingMerchant) GetSessionState(string) (merchant.SessionState, error) {
	return merchant.SessionStateNone, errMerchantStarting
}

func (startingMerchant) AddAllotment(string, string, uint64) (*merchant.CustomerSession, error) {
	return nil, errMerchantStarting
}

func (startingMerchant) GetUsage(string) (string, error) {
	return "", errMerchantStarting
}

func (startingMerchant) Fund(string) (uint64, error) {
	return 0, errMerchantStarting
}

func (startingMerchant) SetOnReachableSetChanged(func()) {}
