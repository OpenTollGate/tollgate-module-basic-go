package wireless_gateway_manager

import "time"

const defaultPortalLoginWindow = 2 * time.Minute
const portalExpiredReason = "portal-login-expired"

type upstreamIdentity struct {
	generation         uint64
	iface, ssid, bssid string
}

type portalDisposition uint8

const (
	portalOrdinary portalDisposition = iota
	portalHold
	portalFallback
	portalSuperseded
	portalDeferred
)

type portalLoginEpisode struct {
	identity    upstreamIdentity
	deadline    time.Time
	expired     bool
	interrupted bool
}

// observePortalLogin consumes an existing verdict, never performs a probe and
// never holds the radio writer while the user logs in. A connection generation
// is the attempt boundary; discovering a BSSID later cannot renew its deadline.
func (um *UpstreamManager) observePortalLogin(id upstreamIdentity, associated bool, result ConnectivityResult) portalDisposition {
	// Try the configured automatic login before taking writerMu. The HTTP
	// attempt can take seconds; holding the radio/UCI writer for it would
	// stall every other operation the manager owns.
	if associated && !result.OK && result.Reason == "captive" {
		if um.tryAutoPortalLogin(id) {
			result = um.probeConnectivity("")
		}
	}
	// Establishing a window and admitting automatic writes share one owner.
	// Never queue the control loop behind a manual DHCP wait. Work already
	// admitted by the owner completes first; a later probe retries observation.
	if !um.writerMu.TryLock() {
		return portalDeferred
	}
	defer um.writerMu.Unlock()
	return um.observePortalLoginUnderWriter(id, associated, result)
}

// observePortalLoginUnderWriter is only for callers already holding writerMu.
func (um *UpstreamManager) observePortalLoginUnderWriter(id upstreamIdentity, associated bool, result ConnectivityResult) portalDisposition {
	um.portalMu.Lock()
	defer um.portalMu.Unlock()
	if um.connectionGeneration.Load() != id.generation {
		return portalSuperseded
	}
	if um.portalLogin.identity.generation != id.generation {
		um.portalLogin = portalLoginEpisode{}
	}
	if um.isTollGateConnection.Load() && um.tollGateGeneration.Load() == id.generation {
		um.portalLogin = portalLoginEpisode{}
		return portalOrdinary
	}
	episode := &um.portalLogin
	if !associated {
		// Do not use another allowance after losing this association without a
		// new connection attempt. Ordinary recovery is immediately eligible.
		episode.interrupted = !episode.deadline.IsZero()
		return portalOrdinary
	}
	if !episode.deadline.IsZero() && (episode.identity.iface != id.iface || episode.identity.ssid != id.ssid) {
		return portalSuperseded
	}
	if result.OK {
		if !episode.deadline.IsZero() {
			logger.WithField("generation", id.generation).Info("Venue login succeeded: internet verified")
		}
		um.portalLogin = portalLoginEpisode{}
		return portalOrdinary
	}
	if episode.interrupted {
		return portalOrdinary
	}
	if episode.deadline.IsZero() && result.Reason == "captive" {
		*episode = portalLoginEpisode{identity: id, deadline: um.now().Add(um.config.PortalLoginWindow)}
		logger.WithFields(map[string]interface{}{"generation": id.generation, "deadline": episode.deadline}).Info("Venue login window started")
	}
	if episode.identity.bssid == "" {
		episode.identity.bssid = id.bssid
	}
	return um.portalDispositionLocked(id.generation)
}

func (um *UpstreamManager) portalLoginDisposition(generation uint64) portalDisposition {
	um.portalMu.Lock()
	defer um.portalMu.Unlock()
	return um.portalDispositionLocked(generation)
}

func (um *UpstreamManager) portalDispositionLocked(generation uint64) portalDisposition {
	if um.connectionGeneration.Load() != generation {
		return portalSuperseded
	}
	episode := &um.portalLogin
	if episode.identity.generation != generation || episode.deadline.IsZero() || episode.interrupted {
		return portalOrdinary
	}
	if um.now().Before(episode.deadline) {
		return portalHold
	}
	if !episode.expired {
		episode.expired = true
		logger.WithField("generation", generation).Info("Venue login window expired: saved fallback eligible")
	}
	return portalFallback
}

func (um *UpstreamManager) portalFallbackDeferred(reason string) {
	logger.WithField("reason", reason).Info("Venue login fallback deferred")
}
