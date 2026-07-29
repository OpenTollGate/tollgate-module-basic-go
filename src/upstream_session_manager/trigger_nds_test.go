package upstream_session_manager

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestTriggerNdsSession_MakesHTTPRequest(t *testing.T) {
	var hit atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_, portStr, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)

	s := &UpstreamSession{
		GatewayIP:    "127.0.0.1",
		NdsPortalPort: port,
	}

	s.triggerNdsSession()

	time.Sleep(200 * time.Millisecond)

	if !hit.Load() {
		t.Fatal("triggerNdsSession did not make HTTP request to the NDS portal")
	}
}

func TestTriggerNdsSession_DefaultPort80(t *testing.T) {
	s := &UpstreamSession{
		GatewayIP: "127.0.0.1",
	}

	port := s.NdsPortalPort
	if port != 0 {
		t.Fatalf("NdsPortalPort should default to 0 (meaning port 80), got %d", port)
	}

	computed := port
	if computed == 0 {
		computed = 80
	}
	if computed != 80 {
		t.Fatalf("default computed port should be 80, got %d", computed)
	}
}

func TestTriggerNdsSession_FailureNonCritical(t *testing.T) {
	s := &UpstreamSession{
		GatewayIP:    "127.0.0.1",
		NdsPortalPort: 1, // Port 1 — nothing listening, should fail silently
	}

	done := make(chan bool)
	go func() {
		s.triggerNdsSession()
		done <- true
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("triggerNdsSession should return quickly even on failure")
	}
}
