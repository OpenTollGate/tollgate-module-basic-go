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

type connectivityHTTPCheck struct {
	url    string
	status int
	body   string
}

// The probe reads these as package-level vars so tests can substitute them
// (and so a future build could override an endpoint) without re-implementing
// the layered logic.
var (
	// connectivityDNSName is resolved to prove the upstream actually forwards
	// DNS. A captive/isolated segment hands out a lease but typically cannot
	// resolve anything outside itself.
	connectivityDNSName = "one.one.one.one"

	// Each endpoint has its own success sentinel. In particular, Firefox's
	// endpoint returns 200 with an exact body, not an empty 204.
	connectivityHTTPChecks = []connectivityHTTPCheck{
		{url: "http://connectivitycheck.gstatic.com/generate_204", status: http.StatusNoContent},
		{url: "http://detectportal.firefox.com/success.txt", status: http.StatusOK, body: "success\n"},
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

	httpResponseFn = func(url string) (int, string, error) {
		client := &http.Client{
			Timeout: 6 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		resp, err := client.Get(url)
		if err != nil {
			return 0, "", err
		}
		defer resp.Body.Close()
		// Read only a bounded body; a portal page or a success prefix with
		// trailing content must not match the endpoint's exact sentinel.
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, string(body), err
	}
)

// probeUpstreamInternet runs the layered check an ICMP ping cannot express:
// default route -> DNS resolution -> endpoint-specific HTTP probe. A network
// that hands out a lease but has no internet fails at DNS or at the HTTP probe,
// and a captive portal is distinguished from a hard failure.
func probeUpstreamInternet() ConnectivityResult {
	out, err := defaultRouteFn()
	if err != nil || !strings.Contains(out, " via ") {
		return ConnectivityResult{OK: false, Reason: "no-route"}
	}

	if err := resolveHostFn(connectivityDNSName); err != nil {
		return ConnectivityResult{OK: false, Reason: "no-dns"}
	}

	for _, check := range connectivityHTTPChecks {
		code, body, err := httpResponseFn(check.url)
		// Only a failure before receiving HTTP headers may try another
		// endpoint. An incomplete portal response must remain terminal.
		if err != nil && code == 0 {
			continue
		}
		if err == nil && code == check.status && body == check.body {
			return ConnectivityResult{OK: true, Reason: "ok"}
		}
		return ConnectivityResult{OK: false, Reason: "captive"}
	}

	return ConnectivityResult{OK: false, Reason: "no-http"}
}
