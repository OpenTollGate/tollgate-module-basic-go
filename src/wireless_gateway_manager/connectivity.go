package wireless_gateway_manager

import (
	"context"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// ConnectivityResult is the outcome of a layered upstream internet probe.
//
// Reason is a short machine-friendly token, not a message for users:
// "ok", "not-associated", "no-route", "no-dns", "captive", "no-http".
type ConnectivityResult struct {
	OK     bool
	Reason string
}

// The probe reads these as package-level vars so tests can substitute them
// (and so a future build could override an endpoint) without re-implementing
// the layered logic.
var (
	// connectivityDNSName is resolved to prove the upstream actually forwards
	// DNS. A captive/isolated segment hands out a lease but typically cannot
	// resolve anything outside itself.
	connectivityDNSName = "one.one.one.one"

	// connectivityCheckURLs are HTTP "captive portal" probes. A plain 204 with
	// no body means the packet reached the real internet; anything else
	// (200/redirect/HTML) is a portal or a blocked response.
	connectivityCheckURLs = []string{
		"http://connectivitycheck.gstatic.com/generate_204",
		"http://detectportal.firefox.com/success.txt",
	}

	defaultRouteFn = func() (string, error) {
		out, err := exec.Command("ip", "route", "show", "default").Output()
		if err != nil {
			return "", err
		}
		return string(out), nil
	}

	resolveHostFn = func(name string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := net.DefaultResolver.LookupHost(ctx, name)
		return err
	}

	httpStatusFn = func(url string) (int, error) {
		client := &http.Client{Timeout: 6 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		// Drain a bounded amount so the connection is cleaned up promptly.
		_, _ = io.CopyN(io.Discard, resp.Body, 4096)
		return resp.StatusCode, nil
	}
)

// probeUpstreamInternet runs the layered check an ICMP ping cannot express:
// default route -> DNS resolution -> HTTP 204 captive-portal probe. A network
// that hands out a lease but has no internet fails at DNS or at the 204 probe,
// and a captive portal is distinguished from a hard failure.
func probeUpstreamInternet() ConnectivityResult {
	out, err := defaultRouteFn()
	if err != nil || !strings.Contains(out, " via ") {
		return ConnectivityResult{OK: false, Reason: "no-route"}
	}

	if err := resolveHostFn(connectivityDNSName); err != nil {
		return ConnectivityResult{OK: false, Reason: "no-dns"}
	}

	for _, url := range connectivityCheckURLs {
		code, err := httpStatusFn(url)
		if err != nil {
			continue
		}
		if code == http.StatusNoContent {
			return ConnectivityResult{OK: true, Reason: "ok"}
		}
		return ConnectivityResult{OK: false, Reason: "captive"}
	}

	return ConnectivityResult{OK: false, Reason: "no-http"}
}
