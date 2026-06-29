package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWhoami_MACLookupFails_Returns200JSON proves that GET /whoami degrades
// gracefully when the caller's MAC address cannot be resolved (no DHCP lease
// and no ARP entry). The captive-portal SPA parses the /whoami response as
// JSON; an HTTP 500 with an empty body makes that parse yield undefined and
// crashes the SPA. The endpoint must instead return HTTP 200 with a JSON body.
//
// Precondition: /tmp/dhcp.leases must be absent so getMacAddress() cannot
// resolve the loopback caller. dnsmasq only writes that file on a router, so
// it never exists on CI/dev machines; we guard explicitly to keep the test
// deterministic and skip (rather than mis-report) when a real lease file is
// present on a router-under-test.
func TestWhoami_MACLookupFails_Returns200JSON(t *testing.T) {
	if _, err := os.Stat("/tmp/dhcp.leases"); err == nil {
		t.Skip("/tmp/dhcp.leases exists on this host; cannot deterministically " +
			"trigger MAC-lookup failure — remove it to run this test")
	}

	// Sanity guard: confirm the failure path is actually reachable for the
	// loopback address before asserting on the HTTP behaviour.
	if _, err := getMacAddress("127.0.0.1"); err == nil {
		t.Skip("getMacAddress resolved 127.0.0.1 on this host; cannot " +
			"deterministically trigger the MAC-lookup failure path")
	}

	// Request originates from localhost. getIP() returns 127.0.0.1 (no
	// forwarding headers), which getMacAddress() cannot resolve.
	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()

	// Invoke the same handler chain registered for the /whoami route in main().
	CorsMiddleware(handler)(w, req)

	// Must NOT be a 500 with an empty body (the bug).
	assert.NotEqual(t, http.StatusInternalServerError, w.Code,
		"/whoami must not return HTTP 500 when MAC lookup fails")
	assert.NotEmpty(t, w.Body.String(),
		"/whoami must not return an empty body when MAC lookup fails")

	// Must be HTTP 200 with a JSON body the SPA can parse without crashing.
	assert.Equal(t, http.StatusOK, w.Code,
		"/whoami should degrade to HTTP 200 when MAC lookup fails")
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json",
		"/whoami must set a JSON content type")

	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body),
		"/whoami response body must be valid JSON")
	assert.Contains(t, body, "mac",
		`"/whoami JSON must contain a "mac" field`)
	assert.Equal(t, "", body["mac"],
		`"mac" must be empty when the address could not be resolved`)
}
