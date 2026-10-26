package upstream_session_manager

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPostTokenToUpstreamIPv6Loopback proves the payment POST reaches a
// gateway addressed by an IPv6 literal. This is the transport precondition
// for TollGate-over-FIPS: the mesh carries IPv6 ULA addresses, and before
// gatewayURL() the URL rendered as "http://[::1]:2121/" -> invalid host parse
// -> the payment never left the box.
func TestPostTokenToUpstreamIPv6Loopback(t *testing.T) {
	var gotPath, gotToken, gotHost string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHost = r.Host
		buf := make([]byte, 512)
		n, _ := r.Body.Read(buf)
		gotToken = string(buf[:n])
		w.WriteHeader(http.StatusOK)
		// kind-1022 session event with an allotment, as the gateway answers.
		w.Write([]byte(`{"kind":1022,"tags":[["allotment","59392"]]}`))
	}))
	// Bind the test server to IPv6 loopback explicitly.
	addr, _ := net.ResolveTCPAddr("tcp", "[::1]:2121")
	ln, err := net.Listen("tcp", addr.String())
	if err != nil {
		t.Skipf("cannot bind [::1]:2121 (production port in use): %v", err)
	}
	srv.Listener = ln
	srv.Start()
	defer srv.Close()

	// Extract the bare ::1 host:port the server actually got.
	host, _, err := net.SplitHostPort(ln.Addr().String())
	assert.NoError(t, err)
	assert.Equal(t, "::1", host)

	allotment, err := postTokenToUpstream(host, "cashutesttoken123")
	assert.NoError(t, err)
	assert.EqualValues(t, 59392, allotment)
	assert.Equal(t, "/", gotPath)
	assert.Contains(t, gotHost, ":") // bracketed v6 host:port reached the server
	assert.Equal(t, "cashutesttoken123", gotToken)
}
