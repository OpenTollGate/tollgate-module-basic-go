package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/cli"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/identity"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	merchant_types "github.com/OpenTollGate/tollgate-module-basic-go/src/merchant_types"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/upstream_detector"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/upstream_session_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/utils"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/wireless_gateway_manager"
	"github.com/nbd-wtf/go-nostr"
	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

// Module-level logger with pre-configured module field
var mainLogger = logrus.WithField("module", "main")

var ipLimiters = make(map[string]*rate.Limiter)
var ipLimitersMu sync.Mutex

func getIPLimiter(ip string) *rate.Limiter {
	ipLimitersMu.Lock()
	defer ipLimitersMu.Unlock()
	limiter, exists := ipLimiters[ip]
	if !exists {
		rpm := 10
		if v := os.Getenv("TOLLGATE_RATE_LIMIT_RPM"); v != "" {
			if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
				rpm = parsed
			}
		}
		limiter = rate.NewLimiter(rate.Every(time.Minute/time.Duration(rpm)), rpm)
		ipLimiters[ip] = limiter
	}
	return limiter
}

func RateLimitMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := getIP(r)
		if !getIPLimiter(ip).Allow() {
			mainLogger.WithField("ip", ip).Warn("Rate limit exceeded")
			w.Header().Set("Retry-After", "6")
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

// --- POST /ln-invoice backpressure -----------------------------------------
//
// POST /ln-invoice is unauthenticated, mutates durable state and makes a round
// trip to a mint, so a caller can make the router work (and make the mint answer
// 429) by looping it. GET /ln-invoice is the status poll a paying customer sits
// in front of, so the quota below is applied to the POST only: a limiter around
// the whole route would throttle a customer mid-payment on their own poll loop.

const (
	// The quote request struct is ~120 bytes on the wire; 8 KiB leaves room for
	// additive fields without letting a caller stream an arbitrary body (and so
	// an arbitrary allocation) into the router.
	maxLightningInvoiceBodyBytes = 8 << 10

	// Ceiling on a single invoice, in sats — roughly the largest plausible
	// purchase. It also bounds what reaches calculateAllotment's arithmetic.
	maxLightningInvoiceSats = 1_000_000

	// Stable refusal codes, additive to the status/error pair the shipped portal
	// already reads, so an operator can tell "we are being flooded" from "the
	// network is slow" without reading logs.
	codeQuoteRateLimited = "quote-rate-limited"
	codeQuoteTableFull   = "quote-table-full"
	codeMintBusyLocal    = "mint-busy-local"
	codeRequestTooLarge  = "request-too-large"
	codeAmountTooLarge   = "amount-too-large"

	// codeAccessGrantFailed is the refusal code for a purchase that was PAID and
	// whose access could not be applied to the enforcement layer. It is
	// deliberately distinct from every lookup/refusal code above: the customer's
	// money is gone in this state, so the answer must tell them not to pay again
	// rather than reading like a generic transient error.
	codeAccessGrantFailed = "access-grant-failed"

	// The MAC every route falls back to when the client cannot be identified.
	// It is not an identity and must never key a quota: two unresolvable clients
	// would share one bucket.
	sentinelMAC = "00:00:00:00:00:00"

	// Hard cap on each quota map. The keys are derived from the socket (see
	// clientLimiterKey), so an attacker cannot choose them freely, but a spoofed
	// MAC or a /64 full of addresses still must not grow the map without bound.
	quoteQuotaMaxKeys = 4096
)

// quoteQuotaLimits are the three POST layers: per client, per source network and
// global. The starting values come from the pre-release design and are all
// env-overridable, like the pre-existing TOLLGATE_RATE_LIMIT_RPM.
type quoteQuotaLimits struct {
	perClientRPM   int
	perClientBurst int
	perSourceRPM   int
	perSourceBurst int
	globalRPS      int
	globalBurst    int
}

func defaultQuoteQuotaLimits() quoteQuotaLimits {
	return quoteQuotaLimits{
		// One quote per purchase; six a minute allows a few legitimate retries
		// after an expired invoice.
		perClientRPM:   envIntOr("TOLLGATE_QUOTE_LIMIT_RPM", 6),
		perClientBurst: envIntOr("TOLLGATE_QUOTE_LIMIT_BURST", 3),
		// A NAT'd group of customers behind one address still works; a flood
		// from one address does not.
		perSourceRPM:   envIntOr("TOLLGATE_QUOTE_SOURCE_LIMIT_RPM", 20),
		perSourceBurst: envIntOr("TOLLGATE_QUOTE_SOURCE_BURST", 5),
		// The business is a toll booth, not a quote exchange: cap what the whole
		// router will do toward the mint, whatever the number of clients.
		globalRPS:   envIntOr("TOLLGATE_QUOTE_GLOBAL_RPS", 2),
		globalBurst: envIntOr("TOLLGATE_QUOTE_GLOBAL_BURST", 5),
	}
}

func envIntOr(name string, fallback int) int {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(v)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

// quoteQuotaEntry is one token bucket plus the last time its key was used, which
// is what makes eviction possible.
type quoteQuotaEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// quoteQuotaState holds the bounded bucket maps.
//
// A mutex-guarded map is the deliberate choice over sync.Map: at these rates (a
// handful of accepted POSTs per second, bounded by the global bucket) the
// critical section is a map lookup plus a token-bucket check — tens of
// nanoseconds — so contention is not measurable, while the single lock gives the
// atomic get-or-create-plus-eviction bookkeeping that sync.Map cannot. Nothing
// here is per-request allocation once a key is warm.
type quoteQuotaState struct {
	limits    quoteQuotaLimits
	mu        sync.Mutex
	perClient map[string]*quoteQuotaEntry
	perSource map[string]*quoteQuotaEntry
	global    *rate.Limiter
}

func newQuoteQuotaState(limits quoteQuotaLimits) *quoteQuotaState {
	return &quoteQuotaState{
		limits:    limits,
		perClient: make(map[string]*quoteQuotaEntry),
		perSource: make(map[string]*quoteQuotaEntry),
		global:    rate.NewLimiter(rate.Limit(limits.globalRPS), limits.globalBurst),
	}
}

// quoteQuotas is the process-wide quota state. It is a package variable so tests
// can point it at their own instance, the same seam shape as dhcpLeasePath.
var quoteQuotas = newQuoteQuotaState(defaultQuoteQuotaLimits())

// allowFrom consumes one token for key from bucket map m, creating the bucket on
// first use and evicting the least-recently-used key when the map is full.
func (s *quoteQuotaState) allowFrom(m map[string]*quoteQuotaEntry, key string, limit rate.Limit, burst int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := m[key]
	if !ok {
		if len(m) >= quoteQuotaMaxKeys {
			evictLeastRecentlyUsedQuotaEntry(m)
		}
		entry = &quoteQuotaEntry{limiter: rate.NewLimiter(limit, burst)}
		m[key] = entry
	}
	entry.lastSeen = time.Now()
	return entry.limiter.Allow()
}

func evictLeastRecentlyUsedQuotaEntry(m map[string]*quoteQuotaEntry) {
	evictOldestKey(m, func(entry *quoteQuotaEntry) time.Time { return entry.lastSeen })
}

// evictOldestKey drops the entry whose timestamp is the oldest, which is the LRU
// ordering both bounded maps in this file use: the quota buckets (last seen when
// the key last spent a token) and the refusal log (last seen when the key last
// logged a line). The scan is O(n) and only runs on an insert once the map is at
// its cap.
func evictOldestKey[V any](m map[string]V, lastSeen func(V) time.Time) {
	var oldestKey string
	var oldest time.Time
	for key, entry := range m {
		if seen := lastSeen(entry); oldestKey == "" || seen.Before(oldest) {
			oldestKey, oldest = key, seen
		}
	}
	delete(m, oldestKey)
}

// allow decides whether a quote-creation request may proceed, returning the
// Retry-After a refusal should advertise. clientKey is the caller's client
// identity, derived once by the middleware and passed in: the derivation reads
// the DHCP lease file and then the ARP table, and a refused request must not pay
// for that lookup twice.
//
// The layers are consumed innermost first: a request that fails the per-client
// bucket never touches the per-source or global bucket, so a flood from one
// client cannot drain the capacity an honest customer draws on, while the global
// bucket still bounds the total for a flood that arrives from many clients at
// once. (The trade is that a request refused by a later layer has already spent
// the earlier layers' tokens — negligible between a 6/min client bucket and a
// 2/s global bucket, and reserving-then-cancelling tokens buys nothing here.)
func (s *quoteQuotaState) allow(clientKey string, r *http.Request) (bool, int) {
	if !s.allowFrom(s.perClient, clientKey,
		rate.Every(time.Minute/time.Duration(s.limits.perClientRPM)), s.limits.perClientBurst) {
		return false, ceilSecondsPerToken(s.limits.perClientRPM, time.Minute)
	}

	if !s.allowFrom(s.perSource, sourceNetworkKey(r),
		rate.Every(time.Minute/time.Duration(s.limits.perSourceRPM)), s.limits.perSourceBurst) {
		return false, ceilSecondsPerToken(s.limits.perSourceRPM, time.Minute)
	}

	s.mu.Lock()
	globalAllowed := s.global.Allow()
	s.mu.Unlock()
	if !globalAllowed {
		return false, ceilSecondsPerToken(s.limits.globalRPS, time.Second)
	}

	return true, 0
}

// ceilSecondsPerToken is the Retry-After for a bucket that admits `count` tokens
// per `window`, rounded up so the advertised wait is never shorter than the wait.
func ceilSecondsPerToken(count int, window time.Duration) int {
	if count <= 0 {
		return 1
	}
	perToken := window / time.Duration(count)
	seconds := (perToken + time.Second - 1) / time.Second
	if seconds < 1 {
		seconds = 1
	}
	return int(seconds)
}

// clientLimiterKey derives the per-client quota key. It is resolved from the
// socket — the DHCP lease, then the ARP table, for the request's source address —
// and never from the request body or a query parameter, because the caller can
// assert any value there. An unresolvable client falls back to the source
// network, never to the sentinel MAC and never to an empty key (which every
// unidentified client would share).
func clientLimiterKey(r *http.Request) string {
	ip := getIP(r)
	if mac, err := getMacAddress(ip); err == nil {
		if normalized := merchant.NormalizeMACAddress(mac); normalized != "" && normalized != sentinelMAC {
			return "mac:" + normalized
		}
	}
	return sourceNetworkKey(r)
}

// clientLimiterKeyFn is the derivation the quota path calls. It is a variable
// only so a test can watch how many times one request derives the client key:
// the derivation reads the DHCP lease file and then the ARP table, and a refused
// request must not pay for that lookup twice. Same seam shape as dhcpLeasePath
// and quoteQuotas — production always leaves it at clientLimiterKey.
var clientLimiterKeyFn = clientLimiterKey

// sourceNetworkKey is the per-source bucket key: the exact address for IPv4 and
// the /64 prefix for IPv6, so a dual-stack LAN cannot mint a fresh bucket for
// every SLAAC address it holds.
func sourceNetworkKey(r *http.Request) string {
	ip := net.ParseIP(getIP(r))
	if ip == nil {
		return "ip:unknown"
	}
	if v4 := ip.To4(); v4 != nil {
		return "ip:" + v4.String()
	}
	return "ip:" + ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// quoteRefusalLog keeps the refusal log line to one per client key per window. A
// warning per refused request is a flood amplifier on the log path, and on a
// router `logread` is the operator's only view during exactly the incident the
// line is supposed to describe. Its map is bounded and LRU-evicted like the
// quota buckets above, so neither the map nor the suppression breaks under a
// flood that arrives from more identities than the cap.
type quoteRefusalLog struct {
	mu     sync.Mutex
	last   map[string]time.Time
	window time.Duration
	count  uint64
}

func (l *quoteRefusalLog) allow(key string) (bool, uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.count++
	if l.last == nil {
		l.last = make(map[string]time.Time)
	}
	if l.window == 0 {
		l.window = 30 * time.Second
	}
	if _, ok := l.last[key]; !ok && len(l.last) >= quoteQuotaMaxKeys {
		// Evict the least recently seen identity, never the whole map: dropping
		// every key reset each other client's window, so a flood from more than
		// quoteQuotaMaxKeys identities re-enabled a log line per refused request
		// for all of them — the log amplification this limiter exists to stop.
		evictOldestKey(l.last, func(seen time.Time) time.Time { return seen })
	}
	if seen, ok := l.last[key]; ok && time.Since(seen) < l.window {
		return false, l.count
	}
	l.last[key] = time.Now()
	return true, l.count
}

var quoteRefusals quoteRefusalLog

// quoteCreateQuotaMiddleware applies the POST-only quota for quote creation.
func quoteCreateQuotaMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Derived once and reused for the refusal log line below. The key comes
		// from the socket (DHCP lease, then ARP table), and a flood of refused
		// requests is the last place to pay for that lookup twice.
		key := clientLimiterKeyFn(r)
		allowed, retryAfter := quoteQuotas.allow(key, r)
		if !allowed {
			if shouldLog, total := quoteRefusals.allow(key); shouldLog {
				mainLogger.WithFields(logrus.Fields{
					"client": key,
					"total":  total,
				}).Warn("ln-invoice quote creation refused: client over quota")
			}
			writeLightningRefusal(w, http.StatusTooManyRequests, codeQuoteRateLimited,
				"This TollGate is busy right now — please retry in a few seconds.", retryAfter)
			return
		}
		next(w, r)
	}
}

// writeLightningRefusal answers a `/ln-invoice` request with a refusal the shipped
// portal can still parse: the `status`/`error` pair it reads, as JSON, plus the
// additive `code` and `retry_after`.
func writeLightningRefusal(w http.ResponseWriter, status int, code, message string, retryAfter int) {
	w.Header().Set("Content-Type", "application/json")
	if retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(lightningInvoiceResponse{
		Status:     0,
		Error:      message,
		Code:       code,
		RetryAfter: retryAfter,
	})
}

// Global configuration variable
// Define configFile at a higher scope
var (
	configManager   *config_manager.ConfigManager
	mainConfig      *config_manager.Config
	installConfig   *config_manager.InstallConfig
	sharedConnector *wireless_gateway_manager.Connector
	sharedScanner   *wireless_gateway_manager.Scanner
)

var upstreamManager *wireless_gateway_manager.UpstreamManager

var tollgateDetailsString string

var (
	merchantProvider *merchantTypesProvider
)

var cliServer *cli.CLIServer

type merchantTypesProvider struct {
	inner *merchant.MutexMerchantProvider
}

func (p *merchantTypesProvider) GetMerchant() merchant_types.PaymentMerchant {
	return p.inner.GetMerchant()
}

func swapMerchant(newMerchant merchant_types.PaymentMerchant) {
	if mi, ok := newMerchant.(merchant.MerchantInterface); ok {
		merchantProvider.inner.SetMerchant(mi)
	} else {
		mainLogger.Error("swapMerchant: cannot convert PaymentMerchant to MerchantInterface")
	}
}

func registerReachableSetChangedCallback(m merchant.MerchantInterface) {
	m.SetOnReachableSetChanged(func() {
		mainLogger.Info("Reachable mint set changed — rebuilding merchant")
		current := merchantProvider.inner.GetMerchant()
		full, ok := current.(*merchant.Merchant)
		if !ok {
			return
		}
		reachableMints := full.GetMintHealthTracker().GetReachableMintConfigs()
		if len(reachableMints) > 0 {
			// A configured mint that was unreachable at boot may have
			// recovered — grow the wallet's accepted set so its tokens
			// are no longer rejected (#481).
			full.AdmitReachableMints()
			return
		}
		mainLogger.Warn("All mints unreachable — downgrading to degraded mode")
		if err := full.Shutdown(); err != nil {
			mainLogger.WithError(err).Error("Failed to shutdown merchant before downgrade")
		}
		deg := merchant.NewMerchantDegradedFromFull(configManager, full.GetMintHealthTracker())
		deg.OnUpgrade(func(upgraded merchant.MerchantInterface) {
			mainLogger.Info("Upgrading from degraded to full merchant after recovery")
			swapMerchant(upgraded)
			registerReachableSetChangedCallback(upgraded)
		})
		// The runtime downgrade must also wire the recovery trigger — the
		// onUpgrade consumer above fires only when the tracker's
		// first-reachable callback is registered; without this the service
		// stays degraded until manually restarted (#400).
		deg.WireRecoveryTrigger()
		// Arm the aggressive probe loop on the downgrade itself: recovery
		// otherwise waits for the next 5-minute proactive cycle (~13 min
		// stuck-degraded observed live); the aggressive loop fires the same
		// first-reachable callback within seconds of the mint returning
		// (#429).
		full.GetMintHealthTracker().ArmAggressiveRetry()
		swapMerchant(deg)
	})
}

// getTollgatePaths returns the configuration file paths based on the environment.
// If TOLLGATE_TEST_CONFIG_DIR is set, it uses paths within that directory for testing.
// Otherwise, it defaults to /etc/tollgate.
func getTollgatePaths() (configPath, installPath, identitiesPath string) {
	if testDir := os.Getenv("TOLLGATE_TEST_CONFIG_DIR"); testDir != "" {
		configPath = filepath.Join(testDir, "config.json")
		installPath = filepath.Join(testDir, "install.json")
		identitiesPath = filepath.Join(testDir, "identities.json")
		return
	}
	// Default paths for production
	configPath = "/etc/tollgate/config.json"
	installPath = "/etc/tollgate/install.json"
	identitiesPath = "/etc/tollgate/identities.json"
	return
}

func InitializeGlobalLogger(logLevel string) {
	level, err := logrus.ParseLevel(strings.ToLower(logLevel))
	if err != nil {
		// Default to info level if parsing fails
		level = logrus.InfoLevel
		logrus.WithError(err).Warn("Failed to parse log level, defaulting to info")
	}

	logrus.SetLevel(level)

	// Set a consistent formatter for the entire application
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
		ForceColors:   true,
	})

	logrus.WithField("log_level", level.String()).Info("Global logger initialized")
}

func init() {
	// --version must not depend on config state: answer before the
	// config manager init below, which is fatal on a broken config.
	if versionRequested(os.Args) {
		fmt.Printf("tollgate-wrt %s\n", cli.Version)
		os.Exit(0)
	}

	http.DefaultTransport = &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second,
		}).DialContext,
		DisableKeepAlives:     true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		ForceAttemptHTTP2:     false,
	}
	http.DefaultClient.Timeout = 30 * time.Second

	var err error

	configPath, installPath, identitiesPath := getTollgatePaths()

	configManager, err = config_manager.NewConfigManager(configPath, installPath, identitiesPath)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create config manager")
	}

	installConfig = configManager.GetInstallConfig()

	mainConfig = configManager.GetConfig()

	InitializeGlobalLogger(mainConfig.LogLevel)

	if mainConfig.RedirectURL != "" {
		delaySeconds := mainConfig.AuthDelaySeconds
		if delaySeconds <= 0 {
			delaySeconds = 8
		}
		valve.AuthDelay = time.Duration(delaySeconds) * time.Second
		mainLogger.WithFields(logrus.Fields{
			"redirect_url": mainConfig.RedirectURL,
			"auth_delay":   valve.AuthDelay,
			"auth_delay_source": func() string {
				if mainConfig.AuthDelaySeconds > 0 {
					return "config"
				}
				return "default"
			}(),
		}).Info("Post-payment redirect enabled, delaying auth for redirect chain")
	}

	sharedConnector = &wireless_gateway_manager.Connector{}
	if mainConfig != nil && mainConfig.UpstreamWifi.DHCPTimeoutSeconds > 0 {
		sharedConnector.DHCPTimeout = time.Duration(mainConfig.UpstreamWifi.DHCPTimeoutSeconds) * time.Second
	}
	sharedScanner = &wireless_gateway_manager.Scanner{Connector: sharedConnector}

	mainLogger.WithField("ip_randomized", installConfig.IPAddressRandomized).Info("Configuration loaded")

	var err2 error
	merchantInstance, err2 := merchant.New(configManager)
	if err2 != nil {
		mainLogger.WithError(err2).Fatal("Failed to create merchant")
	}
	merchantProvider = &merchantTypesProvider{inner: merchant.NewMutexMerchantProvider(merchantInstance)}

	if deg, ok := merchantInstance.(*merchant.MerchantDegraded); ok {
		mainLogger.Warn("Merchant started in degraded mode — wallet will initialize when a mint becomes reachable")
		deg.OnUpgrade(func(full merchant.MerchantInterface) {
			mainLogger.Info("Upgrading from degraded to full merchant")
			swapMerchant(full)
			registerReachableSetChangedCallback(full)
		})
	} else {
		registerReachableSetChangedCallback(merchantInstance)
	}

	initUpstreamManager()

	initUpstreamDetector()

	initCLIServer()
}

func initUpstreamDetector() {
	upstreamDetectorInstance, err := upstream_detector.NewUpstreamDetector(configManager)
	if err != nil {
		mainLogger.WithError(err).Fatal("Failed to create upstream detector instance")
	}

	usmInstance, err := upstream_session_manager.NewUpstreamSessionManager(configManager, merchantProvider)
	if err != nil {
		mainLogger.WithError(err).Fatal("Failed to create upstream session manager instance")
	}
	upstreamDetectorInstance.SetUpstreamSessionManager(usmInstance)

	go func() {
		err := upstreamDetectorInstance.Start()
		if err != nil {
			mainLogger.WithError(err).Error("Error starting upstream detector")
		}
	}()

	mainLogger.Info("UpstreamDetector module initialized with upstream session manager and monitoring network changes")
}

func initUpstreamManager() {
	upstreamConfig := wireless_gateway_manager.DefaultUpstreamManagerConfig()

	cfg := configManager.GetConfig()
	if cfg != nil && cfg.UpstreamWifi.ScanIntervalSeconds > 0 {
		upstreamConfig = wireless_gateway_manager.UpstreamManagerConfig{
			ScanInterval:           time.Duration(cfg.UpstreamWifi.ScanIntervalSeconds) * time.Second,
			FastCheck:              time.Duration(cfg.UpstreamWifi.FastCheckSeconds) * time.Second,
			LostThreshold:          cfg.UpstreamWifi.LostThreshold,
			HysteresisDB:           cfg.UpstreamWifi.HysteresisDB,
			SignalFloor:            cfg.UpstreamWifi.SignalFloor,
			BlacklistTTL:           time.Duration(cfg.UpstreamWifi.BlacklistTTLMinutes) * time.Minute,
			EmergencyPenalty:       cfg.UpstreamWifi.EmergencyPenalty,
			MaxConsecutiveFailures: cfg.UpstreamWifi.MaxConsecutiveFailures,
			SwitchCooldown:         time.Duration(cfg.UpstreamWifi.SwitchCooldownMinutes) * time.Minute,
			StartupGracePeriod:     time.Duration(cfg.UpstreamWifi.StartupGraceSeconds) * time.Second,
			PostSwitchWait:         time.Duration(cfg.UpstreamWifi.PostSwitchWaitSeconds) * time.Second,
		}
	}

	resellerChecker := &resellerModeAdapter{cm: configManager}

	upstreamManager = wireless_gateway_manager.NewUpstreamManager(sharedConnector, sharedScanner, resellerChecker, upstreamConfig)

	go func() {
		upstreamManager.Start(context.Background())
	}()

	mainLogger.Info("Upstream WiFi manager initialized")
}

type resellerModeAdapter struct {
	cm *config_manager.ConfigManager
}

func (r *resellerModeAdapter) IsResellerModeActive() bool {
	if r.cm == nil {
		return false
	}
	cfg := r.cm.GetConfig()
	return cfg != nil && cfg.ResellerMode
}

func initCLIServer() {
	cliServer = cli.NewCLIServer(configManager, merchantProvider.inner, sharedConnector, sharedScanner, upstreamManager)

	err := cliServer.Start()
	if err != nil {
		mainLogger.WithError(err).Error("Failed to start CLI server")
		return
	}

	mainLogger.Info("CLI server initialized and listening on Unix socket")
}

// Lookup sources for getMacAddress, as package-level vars rather than string
// literals so unit tests running off-router (no dnsmasq lease file, no kernel
// ARP table) can point the resolver at a fixture and exercise the real
// resolution path. Production never reassigns them; nothing else reads them.
// Do not "simplify" these back to literals: /balance's session-bearing branch —
// the body a paying customer gets — is only reachable in a unit test through
// this seam (see TestBalanceEndpointLiveSessionReportsUsage).
var (
	dhcpLeasePath = "/tmp/dhcp.leases"
	arpTablePath  = "/proc/net/arp"
)

// sentinelMACAddress is the all-zero address dnsmasq and the kernel ARP table
// use to mean "no address at all". Every route that could not resolve a client
// used to substitute it and carry on, which collapsed all unidentifiable
// clients into ONE shared identity — a shared session record, byte meter,
// lightning quote and open gate — and, on POST / and POST /ln-invoice, charged
// a customer for a device that does not exist. It is never an identity.
const sentinelMACAddress = "00:00:00:00:00:00"

// errDeviceUnresolvedCode is the machine-readable refusal code for a request
// that needs an identity and cannot get one. It is additive next to the
// `status`/`error` pair every portal already parses, so the pinned portal can
// show the message and the operator can distinguish "we could not identify your
// device" from "the mint is down" in the logs and in a bug report.
const errDeviceUnresolvedCode = "device-unresolved"

// deviceUnresolvedMessage is what a customer who cannot be identified is told.
// It is actionable on the device the customer is holding, which is the only
// place the problem can be fixed.
const deviceUnresolvedMessage = "We could not identify your device on the network. Reconnect to the TollGate Wi-Fi and try again."

// isUnknownMAC reports whether mac carries no usable client identity: the empty
// string (the lookup failed) or the all-zero sentinel. Callers that need an
// identity must refuse the request; callers that only echo an identity must
// answer an empty one.
func isUnknownMAC(mac string) bool {
	normalized := merchant.NormalizeMACAddress(mac)
	return normalized == "" || normalized == sentinelMACAddress
}

// errDeviceUnresolved is returned when a request that needs to know which client
// is asking cannot be attributed: the source IP is in neither the DHCP lease
// file nor the kernel ARP table, or the address found there is "no address".
var errDeviceUnresolved = errors.New(errDeviceUnresolvedCode)

// clientMACFromSocket resolves the requesting client's MAC address from the
// request's source IP — the one input a client cannot choose — through the
// dnsmasq lease file and the kernel ARP table, and returns it canonicalised.
//
// It ignores every client-supplied value on purpose: the `mac` query parameter,
// the `mac` field of the POST /ln-invoice body, and any other claim that is not
// derived from the socket. A MAC is visible to anyone on the air and any client
// can put any value in a query string, so honouring one let a client name
// another device's identity — its session, its byte meter, its lightning quote —
// and let the portal cache a stale value across a MAC rotation (the portal reads
// its address once per page load from /whoami and then echoes it back on the
// Lightning lane; that lane has been observed sending `?mac=00:00:00:00:00:00`,
// which now has no effect at all).
//
// The client's claim is not logged, deliberately: it is attacker-controlled
// text, and echoing it into the log would hand a flood a cheap log-amplification
// primitive on a router whose log is read over a slow console.
func clientMACFromSocket(r *http.Request) (string, error) {
	ip := getIP(r)

	mac, err := getMacAddress(ip)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errDeviceUnresolved, err)
	}

	mac = merchant.NormalizeMACAddress(mac)
	if isUnknownMAC(mac) {
		return "", fmt.Errorf("%w: %s resolved to %q", errDeviceUnresolved, ip, mac)
	}

	return mac, nil
}

func getMacAddress(ipAddress string) (string, error) {
	if net.ParseIP(ipAddress) == nil {
		return "", fmt.Errorf("invalid IP address: %s", ipAddress)
	}
	ipLower := strings.ToLower(strings.TrimSpace(ipAddress))

	// Primary source: dnsmasq lease file — authoritative for DHCP clients.
	// Format per line: <timestamp> <mac> <ip> <hostname> <clientid>
	// Match case-insensitively so IPv6 hextets compare regardless of casing.
	data, err := os.ReadFile(dhcpLeasePath)
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 3 && strings.ToLower(fields[2]) == ipLower {
				return strings.TrimSpace(fields[1]), nil
			}
		}
	}

	// Fallback: kernel ARP table — catches static-IP clients and survives
	// dnsmasq restarts. In-memory entries expire after a few minutes of
	// inactivity, so this is not a replacement for the lease file.
	// Format per line: <ip> <hwtype> <flags> <mac> <mask> <device>
	arpData, err := os.ReadFile(arpTablePath)
	if err == nil {
		for _, line := range strings.Split(string(arpData), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 4 && strings.ToLower(fields[0]) == ipLower && fields[3] != "00:00:00:00:00:00" {
				return strings.TrimSpace(fields[3]), nil
			}
		}
	}

	return "", fmt.Errorf("no MAC found for %s in DHCP leases or ARP table", ipAddress)
}

// CORS middleware to handle Cross-Origin Resource Sharing
func CorsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mainLogger.WithFields(logrus.Fields{
			"method":      r.Method,
			"remote_addr": r.RemoteAddr,
		}).Debug("CORS middleware processing request")

		// CORS policy (security): mirrors the Rust module's cors_response —
		// echo the Origin only for local/private or same-host origins (the
		// router's own portal is cross-origin once served from uhttpd :2051).
		// Never a wildcard: this API is LAN-firewall-protected, not
		// credential-protected, so "*" would let any website read responses
		// from a browser on the TollGate network (OWASP).
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		origin := r.Header.Get("Origin")
		if origin != "" && origin != "null" && (isLocalOrigin(origin) || isSameHost(origin, r.Host)) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			// Cache-safe: the echo decision varies per Origin (MDN).
			w.Header().Add("Vary", "Origin")
		}

		// Handle preflight OPTIONS requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Call the next handler
		next(w, r)
	}
}

func handler(w http.ResponseWriter, r *http.Request) {
	// The portal calls this once per page load to learn which device it is
	// looking at. The answer comes from the socket, never from a `mac` parameter
	// the caller supplied.
	mac, err := clientMACFromSocket(r)
	if err != nil {
		// Not fatal here: /whoami is an echo of the caller's own address, not a
		// request that needs one (the money routes refuse an unidentified
		// client). Answer an empty mac instead of returning 500 — and never the
		// sentinel, which publishes 00:00:00:00:00:00 as if it were an identity.
		mainLogger.WithError(err).Warn("MAC address lookup failed for /whoami; answering an empty mac")
		mac = ""
	}

	mainLogger.WithField("mac", mac).Debug("MAC address resolved")
	fmt.Fprint(w, "mac=", mac)
}

func handleDetails(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, merchantProvider.inner.GetMerchant().GetAdvertisement())
}

// NUT #00: `cashu` is the Cashu token prefix. `[version]` is a single `base64_urlsafe` character to denote the token format version.

// handleRootPost handles POST requests to the root endpoint
func extractCashuToken(body []byte) (token string, event *nostr.Event) {
	var ev nostr.Event
	err := json.Unmarshal(body, &ev)
	if err == nil && ev.Kind == 21000 {
		for _, tag := range ev.Tags {
			if len(tag) >= 2 && tag[0] == "payment" {
				return tag[1], &ev
			}
		}
		return "", &ev
	}
	return strings.TrimSpace(string(body)), nil
}

// NUT #00: Serialized tokens have a Cashu token prefix, a versioning flag, and the token.
func HandleRootPost(w http.ResponseWriter, r *http.Request) {
	// Log the request details
	mainLogger.WithFields(logrus.Fields{
		"method":      r.Method,
		"remote_addr": r.RemoteAddr,
	}).Info("Received handleRootPost request")
	// Only process POST requests
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	contentType := r.Header.Get("Content-Type")
	if contentType != "text/plain" && contentType != "application/json" && contentType != "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnsupportedMediaType)
		json.NewEncoder(w).Encode(map[string]string{"error": "Unsupported Media Type"})
		return
	}

	// Get the client's identity from the socket. A `mac` query parameter is not
	// consulted: its value is the caller's claim about itself, and on this route
	// it decides which device the grant is applied to.
	//
	// This is the money path, where a wrong identity cannot be recovered: the
	// token is received before the gate is opened, so a request that names
	// 00:00:00:00:00:00 — or that cannot be resolved at all — would consume the
	// customer's value and grant nothing (the rollback happens after Receive).
	// Refuse BEFORE the token is read, with a distinct code the portal can show.
	macAddress, err := clientMACFromSocket(r)
	if err != nil {
		mainLogger.WithError(err).WithField("remote_addr", r.RemoteAddr).
			Warn("Payment refused: the client has no resolvable identity")
		sendNoticeResponse(w, merchantProvider.inner.GetMerchant(), http.StatusBadRequest, "error", errDeviceUnresolvedCode,
			deviceUnresolvedMessage, "")
		return
	}

	// Read the request body (capped at 1MB to prevent resource exhaustion)
	defer r.Body.Close()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		mainLogger.WithError(err).Error("Request body read failed")
		sendNoticeResponse(w, merchantProvider.inner.GetMerchant(), http.StatusBadRequest, "error", "invalid-request",
			"Invalid request", macAddress)
		return
	}

	// The body IS the bearer instrument on this route: extractCashuToken returns
	// it verbatim unless it is a kind-21000 event, so a log line carrying it is a
	// spendable token — and debug is the level an operator turns on precisely when
	// a payment needs diagnosing, i.e. when the log gets copied into a bug report.
	// Log the length and a salted fingerprint instead: enough to follow one
	// payment through the log and match it against what the customer reports,
	// useless to anyone reading the log.
	mainLogger.WithFields(logrus.Fields{
		"body_len": len(body),
		"body_sha": utils.TokenFingerprint(string(body)),
	}).Debug("Received POST request")

	cashuToken, nostrEvent := extractCashuToken(body)

	if nostrEvent != nil {
		mainLogger.WithFields(logrus.Fields{
			"event_id":   nostrEvent.ID,
			"created_at": nostrEvent.CreatedAt,
			"kind":       nostrEvent.Kind,
			"pubkey":     nostrEvent.PubKey,
		}).Info("Parsed nostr event (signature not validated)")

		if cashuToken == "" {
			mainLogger.Error("No payment tag found in event")
			sendNoticeResponse(w, merchantProvider.inner.GetMerchant(), http.StatusBadRequest, "error", "invalid-event",
				"No payment tag found in event", macAddress)
			return
		}
	} else {
		mainLogger.Info("Treating request as plain Cashu token string")
	}

	// Process payment with cashu token and MAC address
	responseEvent, err := merchantProvider.inner.GetMerchant().PurchaseSession(cashuToken, macAddress)

	// Set response headers
	w.Header().Set("Content-Type", "application/json")

	if err != nil {
		mainLogger.WithError(err).Error("Payment processing failed")
		sendNoticeResponse(w, merchantProvider.inner.GetMerchant(), http.StatusInternalServerError, "error", "internal-error",
			"Payment processing failed", macAddress)
		return
	}

	// Check if the response is a notice event (kind 21023) or session event (kind 1022)
	if responseEvent.Kind == 21023 {
		// It's a notice event (error case), return with appropriate status
		w.WriteHeader(http.StatusBadRequest)
		err = json.NewEncoder(w).Encode(responseEvent)
	} else {
		// It's a session event (success case), return with OK status
		w.WriteHeader(http.StatusOK)
		err = json.NewEncoder(w).Encode(responseEvent)
	}

	if err != nil {
		mainLogger.WithError(err).Error("Error encoding session response")
	}

}

// sendNoticeResponse creates and sends a notice event response
func sendNoticeResponse(w http.ResponseWriter, m merchant.MerchantInterface, statusCode int, level, code, message, customerPubkey string) {
	noticeEvent, err := m.CreateNoticeEvent(level, code, message, customerPubkey)
	if err != nil {
		mainLogger.WithError(err).Error("Error creating notice event")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "Internal server error"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(noticeEvent)
}

// handleRoot routes requests based on method
func HandleUsage(w http.ResponseWriter, r *http.Request) {
	ip := getIP(r)
	macAddress, err := getMacAddress(ip)
	if err != nil {
		mainLogger.WithError(err).Error("Error getting MAC address for /usage")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "-1/-1")
		return
	}
	usageStr, err := merchantProvider.inner.GetMerchant().GetUsage(macAddress)
	if err != nil {
		mainLogger.WithFields(logrus.Fields{
			"mac":   macAddress,
			"error": err,
		}).Error("Error getting usage")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "-1/-1")
		return
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, usageStr)
}

func HandleRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		HandleRootPost(w, r)
	} else {
		handleDetails(w, r)
	}
}

type lightningInvoiceRequest struct {
	Amount  uint64 `json:"amount"`
	MintURL string `json:"mint_url"`
	Mint    string `json:"mint"`
	// Mac is accepted for wire compatibility with the pinned portal, which sends
	// the address it read from /whoami, and is deliberately ignored: identity is
	// resolved from the socket (clientMACFromSocket). The field is kept so the
	// request still decodes and so the intent is visible to the next reader —
	// removing it would silently make the same behaviour look accidental.
	Mac string `json:"mac"`
}

type lightningInvoiceResponse struct {
	Status        int    `json:"status"`
	Quote         string `json:"quote"`
	Invoice       string `json:"invoice,omitempty"`
	MintURL       string `json:"mint_url"`
	Amount        uint64 `json:"amount"`
	Expiry        uint64 `json:"expiry,omitempty"`
	State         string `json:"state"`
	AccessGranted bool   `json:"access_granted"`
	Allotment     uint64 `json:"allotment,omitempty"`
	Metric        string `json:"metric,omitempty"`
	Error         string `json:"error,omitempty"`
	// Code is the machine-readable refusal reason, additive to the
	// `status`/`error` pair the shipped portal already parses. RetryAfter mirrors
	// the Retry-After header in the body so a portal can render a countdown
	// without reaching for a header it may not be allowed to read.
	Code       string `json:"code,omitempty"`
	RetryAfter int    `json:"retry_after,omitempty"`
}

type balanceResponse struct {
	Status        int    `json:"status"`
	SessionActive bool   `json:"session_active"`
	Metric        string `json:"metric,omitempty"`
	Usage         uint64 `json:"usage"`
	Allotment     uint64 `json:"allotment"`
	Remaining     uint64 `json:"remaining"`
	StartTime     int64  `json:"start_time,omitempty"`
	Error         string `json:"error,omitempty"`
}

// sessionStateResponse is the body of GET /session-state. `state` is the
// machine-readable session state for `mac`: "none" (never had a session),
// "active" (allotment left) or "expired" (had a session that is used up).
type sessionStateResponse struct {
	Status int    `json:"status"`
	Mac    string `json:"mac"`
	State  string `json:"state"`
	Error  string `json:"error,omitempty"`
}

// HandleSessionState serves GET /session-state?mac=… — the session state of one
// client, which `/usage` cannot express: "-1/-1" is the same answer for a
// first-time visitor and for a customer whose paid session just ran out, so a
// portal could not tell them apart and could not offer a renewal.
//
// This endpoint is additive. `/usage` keeps answering `used/total` and `-1/-1`
// byte for byte because the shipped portal parses exactly those bytes, and
// `/balance` keeps its shape.
func HandleSessionState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// The state of the client at the other end of the socket — the `mac` query
	// parameter this route used to accept is a claim by the caller about some
	// other device, and answering it let one client read another's state.
	macAddress, err := clientMACFromSocket(r)
	if err != nil {
		// An unidentifiable client has no session, and the portal polls this
		// while rendering — so answer "none" rather than erroring. The sentinel
		// (and an empty lookup) is not a client either: the mac field stays in
		// the response shape but is empty, because echoing 00:00:00:00:00:00
		// published "unknown" as if it were an address (the leak observed on the
		// published pre15 artifact).
		mainLogger.WithError(err).Warn("MAC address lookup failed for /session-state; answering none")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(sessionStateResponse{Status: 1, Mac: "", State: string(merchant.SessionStateNone)})
		return
	}

	state, err := merchantProvider.inner.GetMerchant().GetSessionState(macAddress)
	if err != nil {
		mainLogger.WithFields(logrus.Fields{"mac": macAddress, "error": err}).Error("Error getting session state")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(sessionStateResponse{Status: 0, Mac: macAddress, Error: "failed to retrieve session state"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(sessionStateResponse{Status: 1, Mac: macAddress, State: string(state)})
}

func parseUsageString(usage string) (uint64, uint64, error) {
	parts := strings.Split(strings.TrimSpace(usage), "/")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid usage format: %s", usage)
	}

	used, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid usage value: %w", err)
	}

	allotment, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid allotment value: %w", err)
	}

	return used, allotment, nil
}

func HandleBalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	ip := getIP(r)
	macAddress, err := getMacAddress(ip)
	if err != nil {
		// Client IP not in DHCP leases — can't identify device.
		// Return "no active session" instead of erroring, so the balance
		// page works even when lease is expired or missing (e.g. after
		// dnsmasq restart, or requests from non-DHCP clients).
		mainLogger.WithError(err).Debug("MAC lookup failed for /balance, returning no-session")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(balanceResponse{Status: 1, SessionActive: false})
		return
	}

	usage, err := merchantProvider.inner.GetMerchant().GetUsage(macAddress)
	if err != nil {
		mainLogger.WithFields(logrus.Fields{"mac": macAddress, "error": err}).Error("Error getting balance usage")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(balanceResponse{Status: 0, Error: "failed to retrieve usage data"})
		return
	}
	if usage == "-1/-1" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(balanceResponse{Status: 1, SessionActive: false})
		return
	}

	used, allotment, err := parseUsageString(usage)
	if err != nil {
		mainLogger.WithFields(logrus.Fields{"mac": macAddress, "usage": usage, "error": err}).Error("Error parsing usage string")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(balanceResponse{Status: 0, Error: "failed to process usage data"})
		return
	}

	session, err := merchantProvider.inner.GetMerchant().GetSession(macAddress)
	if err != nil || session == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(balanceResponse{Status: 1, SessionActive: false})
		return
	}

	remaining := uint64(0)
	if allotment > used {
		remaining = allotment - used
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(balanceResponse{
		Status:        1,
		SessionActive: true,
		Metric:        session.Metric,
		Usage:         used,
		Allotment:     allotment,
		Remaining:     remaining,
		StartTime:     session.StartTime,
	})
}

func HandleLightningInvoice(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		handleLightningInvoicePost(w, r)
	case http.MethodGet:
		handleLightningInvoiceGet(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleLNInvoiceRoute is the `/ln-invoice` entry point registered by main. It
// exists so the two halves of the endpoint are dispatched explicitly: the POST
// (quote creation) and the GET (status poll) have different cost profiles and
// must not share a middleware chain.
func handleLNInvoiceRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		// Quote creation only. The GET below is the poll loop a customer sits in
		// front of while paying: on any cadence the pinned portal SPA uses, it
		// must never be throttled, or the quota breaks the flow it protects.
		quoteCreateQuotaMiddleware(HandleLightningInvoice)(w, r)
		return
	}
	HandleLightningInvoice(w, r)
}

func handleLightningInvoicePost(w http.ResponseWriter, r *http.Request) {
	// Bound the body before parsing it. The request is ~120 bytes; MaxBytesReader
	// makes an oversized one a 413 instead of an allocation.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLightningInvoiceBodyBytes))
	if err != nil {
		mainLogger.WithError(err).Warn("Rejected oversized /ln-invoice request body")
		writeLightningRefusal(w, http.StatusRequestEntityTooLarge, codeRequestTooLarge,
			"Request body too large.", 0)
		return
	}

	var req lightningInvoiceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(lightningInvoiceResponse{Status: 0, Error: "invalid request body"})
		return
	}

	mintURL := strings.TrimSpace(req.MintURL)
	if mintURL == "" {
		mintURL = strings.TrimSpace(req.Mint)
	}
	if req.Amount == 0 || mintURL == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(lightningInvoiceResponse{Status: 0, Error: "amount and mint_url are required"})
		return
	}
	if req.Amount > maxLightningInvoiceSats {
		mainLogger.WithField("amount", req.Amount).Warn("Rejected /ln-invoice amount above the ceiling")
		writeLightningRefusal(w, http.StatusBadRequest, codeAmountTooLarge,
			fmt.Sprintf("Amount too large: this TollGate accepts at most %d sats per invoice.", maxLightningInvoiceSats), 0)
		return
	}

	// The quote is bound to the client at the other end of the socket. The `mac`
	// field above is not read: a caller-named address would bind the quote — and
	// the eventual grant — to a device that may not be the one paying.
	macAddress, err := clientMACFromSocket(r)
	if err != nil {
		// A quote is only meaningful for a device that exists: the status poll
		// and the eventual grant are both bound to this address, and the quote
		// store is keyed by it. Creating one for 00:00:00:00:00:00 (or for an
		// empty lookup) wrote a record every unidentified client shared. Refuse
		// instead, with a code the portal can display.
		mainLogger.WithError(err).WithField("remote_addr", r.RemoteAddr).
			Warn("Refusing lightning invoice: the client has no resolvable identity")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(lightningInvoiceResponse{
			Status: 0,
			Error:  deviceUnresolvedMessage,
			Code:   errDeviceUnresolvedCode,
		})
		return
	}

	invoice, err := merchantProvider.inner.GetMerchant().RequestLightningInvoice(macAddress, mintURL, req.Amount)
	if err != nil {
		mainLogger.WithError(err).Warn("Failed to create lightning invoice")
		switch {
		case errors.Is(err, merchant.ErrTooManyQuotes):
			// A local refusal with a distinct code: the mint was never
			// contacted, so this says "we are being flooded", not "the mint is
			// down", and the client is asked to come back rather than to retry
			// immediately against a full table.
			writeLightningRefusal(w, http.StatusTooManyRequests, codeQuoteTableFull,
				"This TollGate is holding as many unpaid invoices as it can — please retry in a few minutes.", 60)
		case errors.Is(err, merchant.ErrMintBusyLocal):
			// Our own outbound budget toward the mint is spent. Refusing here is
			// what keeps our traffic from being the reason the mint answers 429.
			writeLightningRefusal(w, http.StatusTooManyRequests, codeMintBusyLocal,
				"The mint is receiving as many requests as it accepts right now — please retry in a few seconds.", 2)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(lightningInvoiceResponse{Status: 0, Error: "failed to create lightning invoice"})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(lightningInvoiceResponse{
		Status:        1,
		Quote:         invoice.QuoteID,
		Invoice:       invoice.Invoice,
		MintURL:       invoice.MintURL,
		Amount:        invoice.Amount,
		Expiry:        invoice.Expiry,
		State:         invoice.State,
		AccessGranted: false,
	})
}

func handleLightningInvoiceGet(w http.ResponseWriter, r *http.Request) {
	quoteID := strings.TrimSpace(r.URL.Query().Get("quote"))
	if quoteID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(lightningInvoiceResponse{Status: 0, Error: "quote is required"})
		return
	}

	// The MAC is not a lookup key here, it is the authorisation check: a quote is
	// only readable by the device that created it. It comes from the socket — a
	// `mac` query parameter would let any client name another device and read
	// that device's quote state. A poll that cannot be attributed must be refused
	// rather than attributed to 00:00:00:00:00:00, which every unidentified
	// client would share.
	macAddress, err := clientMACFromSocket(r)
	if err != nil {
		mainLogger.WithError(err).WithField("remote_addr", r.RemoteAddr).
			Warn("Refusing lightning status poll: the client has no resolvable identity")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(lightningInvoiceResponse{
			Status: 0,
			Error:  deviceUnresolvedMessage,
			Code:   errDeviceUnresolvedCode,
		})
		return
	}

	// Quotes are bound to the device MAC at invoice creation time. Polling only
	// reveals status for that same device and access is granted to the recorded MAC.
	status, err := merchantProvider.inner.GetMerchant().GetLightningInvoiceStatus(quoteID, macAddress)
	if err != nil {
		// A PAID purchase whose access could not be applied is NOT a status
		// lookup failure, and answering it like one is how a customer ends up
		// paying and staring at a portal that never says why (measured on the
		// bench MT3000, 2026-09-26: state=PAID, merchant wallet +1 sat,
		// access_granted never true). It is logged at ERROR with the client and
		// the quote, and answered with its own code and a message that tells the
		// customer the payment was received and not to pay again.
		if errors.Is(err, merchant.ErrAccessGrantNotApplied) {
			mainLogger.WithError(err).WithFields(logrus.Fields{
				"quote": quoteID,
				"mac":   macAddress,
			}).Error("A PAID purchase could not be granted: the invoice settled but the access was NOT applied — the client keeps no allotment until the grant succeeds")
			writeLightningRefusal(w, http.StatusServiceUnavailable, codeAccessGrantFailed,
				"Your payment was received, but the router could not open the gate for this device yet. Do NOT pay again — access is retried automatically and opens as soon as the router can apply it.", 15)
			return
		}

		statusCode := http.StatusInternalServerError
		if errors.Is(err, merchant.ErrQuoteNotFound) {
			statusCode = http.StatusNotFound
		}
		mainLogger.WithError(err).Warn("Failed to fetch lightning invoice status")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(lightningInvoiceResponse{Status: 0, Error: "failed to fetch invoice status"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(lightningInvoiceResponse{
		Status:        1,
		Quote:         status.QuoteID,
		MintURL:       status.MintURL,
		Amount:        status.Amount,
		State:         status.State,
		AccessGranted: status.AccessGranted,
		Allotment:     status.Allotment,
		Metric:        status.Metric,
	})
}

// versionRequested reports whether the daemon was invoked with the
// --version flag. The daemon takes no other command-line arguments;
// OpenWrt package CI runs binaries with --version and expects the
// version string on stdout.
func versionRequested(args []string) bool {
	if len(args) < 2 {
		return false
	}
	return args[1] == "--version" || args[1] == "-version"
}

func main() {
	var port = ":2121" // Change from "0.0.0.0:2121" to just ":2121"
	fmt.Println("Starting Tollgate Core")
	fmt.Println("Listening on all interfaces on port", port)

	mainLogger.Info("Registering handlers...")

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mainLogger.WithField("remote_addr", r.RemoteAddr).Debug("Hit / endpoint")
		RateLimitMiddleware(CorsMiddleware(HandleRoot))(w, r)
	})

	http.HandleFunc("/whoami", func(w http.ResponseWriter, r *http.Request) {
		mainLogger.WithField("remote_addr", r.RemoteAddr).Debug("Hit /whoami endpoint")
		CorsMiddleware(handler)(w, r)
	})

	http.HandleFunc("/ln-invoice", func(w http.ResponseWriter, r *http.Request) {
		mainLogger.WithField("remote_addr", r.RemoteAddr).Debug("Hit /ln-invoice endpoint")
		CorsMiddleware(handleLNInvoiceRoute)(w, r)
	})

	http.HandleFunc("/balance", func(w http.ResponseWriter, r *http.Request) {
		mainLogger.WithField("remote_addr", r.RemoteAddr).Debug("Hit /balance endpoint")
		CorsMiddleware(HandleBalance)(w, r)
	})

	http.HandleFunc("/usage", func(w http.ResponseWriter, r *http.Request) {
		mainLogger.WithField("remote_addr", r.RemoteAddr).Debug("Hit /usage endpoint")
		CorsMiddleware(HandleUsage)(w, r)
	})

	http.HandleFunc("/session-state", func(w http.ResponseWriter, r *http.Request) {
		mainLogger.WithField("remote_addr", r.RemoteAddr).Debug("Hit /session-state endpoint")
		CorsMiddleware(HandleSessionState)(w, r)
	})

	// --- Identity derivation (additive, optional) --------------------------
	// Derive network identity (npub, IPv4, MACs, BIP39 seed) from the existing
	// merchant private key in identities.json (owned_identities[0].privatekey).
	// This is a bonus feature: if identities.json is missing, malformed, or has
	// no usable key, the routes are simply not registered and TollGate boots and
	// serves all existing endpoints normally. No existing endpoint is touched.
	identityPrivKey := ""
	if ids := configManager.GetIdentities(); ids != nil && len(ids.OwnedIdentities) > 0 {
		identityPrivKey = ids.OwnedIdentities[0].PrivateKey
	}
	if identityPrivKey == "" {
		mainLogger.Warn("identity: no merchant private key in identities.json — /identity routes disabled")
	} else if _, err := identity.Derive(identityPrivKey); err != nil {
		mainLogger.WithError(err).Warn("identity: merchant private key invalid — /identity routes disabled")
		identityPrivKey = ""
	}
	if identityPrivKey != "" {
		http.HandleFunc("/identity", func(w http.ResponseWriter, r *http.Request) {
			mainLogger.WithField("remote_addr", r.RemoteAddr).Debug("Hit /identity endpoint")
			CorsMiddleware(handleIdentityDerive(identityPrivKey))(w, r)
		})
		// reveal-seed accepts a 12-word BIP39 mnemonic and raw private key —
		// POST-only so the request is intentional and never cached/prefetched.
		http.HandleFunc("/identity/reveal-seed", func(w http.ResponseWriter, r *http.Request) {
			mainLogger.WithField("remote_addr", r.RemoteAddr).Warn("Hit /identity/reveal-seed endpoint (sensitive)")
			CorsMiddleware(handleIdentityRevealSeed(identityPrivKey))(w, r)
		})
		mainLogger.Info("identity: /identity and /identity/reveal-seed routes registered")
	}

	mainLogger.Info("Starting HTTP server on all interfaces...")
	server := &http.Server{
		Addr: port,
		// Add explicit timeouts to avoid potential deadlocks in Go 1.24
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	mainLogger.Fatal(server.ListenAndServe())
}

func isLocalRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

func getIP(r *http.Request) string {
	if isLocalRequest(r) {
		ip := r.Header.Get("X-Real-Ip")
		if ip != "" {
			return strings.TrimSpace(ip)
		}

		ips := r.Header.Get("X-Forwarded-For")
		if ips != "" {
			return strings.TrimSpace(strings.Split(ips, ",")[0])
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

var privateCIDRs []net.IPNet

func init() {
	for _, cidr := range []string{
		"192.168.0.0/16",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"127.0.0.0/8",
		"fd00::/8",
		"::1/128",
	} {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Sprintf("invalid CIDR %s: %v", cidr, err))
		}
		privateCIDRs = append(privateCIDRs, *n)
	}
}

// isSameHost reports whether the Origin's host matches the host the client
// addressed this API by, ignoring port: origins are scheme+host+port, so the
// router's own portal served from another port (uhttpd :2051 calling the API
// on :2121) is cross-origin yet must stay allowed.
func isSameHost(origin, requestHost string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	originHost := strings.ToLower(u.Hostname())
	if originHost == "" {
		return false
	}
	reqHost := strings.ToLower(requestHost)
	if h, _, err := net.SplitHostPort(reqHost); err == nil {
		reqHost = h
	}
	return originHost == reqHost
}

func isLocalOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()

	if ip := net.ParseIP(host); ip != nil {
		for _, n := range privateCIDRs {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	}

	if host == "localhost" {
		return true
	}

	addrs, err := net.LookupHost(host)
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		if ip := net.ParseIP(addr); ip != nil {
			for _, n := range privateCIDRs {
				if n.Contains(ip) {
					return true
				}
			}
		}
	}
	return false
}

// handleIdentityDerive returns an http.HandlerFunc that serves the public,
// non-sensitive derived identity (npub, IPv4, MACs) as JSON for the given
// merchant private key. Registered at GET /identity.
func handleIdentityDerive(privKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		derived, err := identity.Derive(privKey)
		if err != nil {
			mainLogger.WithError(err).Error("identity: derive failed")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(derived)
	}
}

// handleIdentityRevealSeed accepts a 12-word BIP39 mnemonic in the POST body
// and returns the full identity (private key, npub, IPv4, MACs, mnemonic).
// POST-only: a non-POST request gets 405 Method Not Allowed.
// Registered at POST /identity/reveal-seed.
func handleIdentityRevealSeed(_ string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLocalRequest(r) {
			http.Error(w, "forbidden: loopback only", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed: use POST", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1024))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		mnemonic := strings.TrimSpace(string(body))
		full, err := identity.DeriveFromMnemonic(mnemonic)
		if err != nil {
			mainLogger.WithError(err).Error("identity: mnemonic recovery failed")
			http.Error(w, "invalid mnemonic", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(full)
	}
}
