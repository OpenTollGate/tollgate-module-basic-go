package upstream_session_manager

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/sirupsen/logrus"
)

// syncBuffer is a bytes.Buffer that logrus can write to while a test reads from it.
// bytes.Buffer is not safe for concurrent use, and the global logger it stands in
// for is written from goroutines this package does not join.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// captureLogs points the GLOBAL logrus output at a local, concurrency-safe buffer
// for the duration of the test and restores it afterwards.
//
// The buffer has to be synchronized because logrus.StandardLogger() is process-wide
// and this package logs from goroutines a test never joins — `go triggerNdsSession`
// in bootstrap.go is one. Before this helper existed, three tests in this file put a
// plain bytes.Buffer in place of the global output and then read it; when a goroutine
// leaked by an earlier test wrote a log line during that read, `go test -race`
// reported a data race in the TEST, not in the prober:
//
//	Read at 0x... by goroutine 155:  bytes.(*Buffer).String()   prober_content_type_test.go:50
//	Previous write by goroutine 38:  bytes.(*Buffer).grow() <- logrus Entry.write()
//	                                 triggerNdsSession()       session.go:497
//	                                 (*UpstreamSession).ForwardFirstProof.gowrap1()  bootstrap.go:305
//
// That flake failed 4 runs in 5 at untouched base 959280db, which makes -race
// untrustworthy for everyone working on this tree. Serializing the buffer removes the
// report without weakening a single assertion: the tests still read the same text.
//
// t.Cleanup rather than a bare defer, so a t.Fatalf part-way through still restores the
// global logger — leaving the whole test binary writing into a dead buffer would turn
// one failure into a cascade.
func captureLogs(t *testing.T) *syncBuffer {
	t.Helper()
	logs := &syncBuffer{}
	prevOut := logrus.StandardLogger().Out
	prevLevel := logrus.GetLevel()
	logrus.SetOutput(logs)
	logrus.SetLevel(logrus.DebugLevel)
	t.Cleanup(func() {
		logrus.SetOutput(prevOut)
		logrus.SetLevel(prevLevel)
	})
	return logs
}

// #628, client-side root cause. The prober's Content-Type check compared the
// whole header string against "application/json", so every spec-valid
// parameterised form — `application/json; charset=utf-8` being the usual one
// net/http emits — tripped a warning that says the gateway is not a TollGate.
//
// That false warning is what pushed the server header to be pinned to the
// suffix-less value: while this compare is a whole-string compare, any
// future (correct) decision to add a charset on the server side silently
// re-arms the same noisy-warning defect. Compare the parsed media type.
func TestPerformRequest_AcceptsParameterisedJSONContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"advertisement":"probe"}`))
	}))
	defer srv.Close()

	logs := captureLogs(t)

	prober := NewTollGateProber(&config_manager.UpstreamDetectorConfig{}).(*tollGateProber)
	body, err := prober.performRequestWithContext(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("performRequestWithContext: %v", err)
	}
	if !strings.Contains(string(body), "advertisement") {
		t.Fatalf("body = %q, want the JSON advertisement", body)
	}

	if got := logs.String(); strings.Contains(got, "unexpected content type") {
		t.Errorf("prober warned about %q, a spec-valid JSON media type; log output:\n%s",
			"application/json; charset=utf-8", got)
	}
}

// The lenient compare must still be a compare: a body labelled as something
// else has to keep warning, or the check is decoration.
func TestPerformRequest_StillWarnsOnNonJSONContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html>captive portal</html>`))
	}))
	defer srv.Close()

	logs := captureLogs(t)

	prober := NewTollGateProber(&config_manager.UpstreamDetectorConfig{}).(*tollGateProber)
	if _, err := prober.performRequestWithContext(context.Background(), srv.URL); err != nil {
		t.Fatalf("performRequestWithContext: %v", err)
	}

	if got := logs.String(); !strings.Contains(got, "unexpected content type") {
		t.Errorf("prober did not warn on text/html, want a warning; log output:\n%s", got)
	}
}

// A header that is absent entirely is still tolerated (the check is advisory),
// and a header that does not parse must not be treated as JSON.
func TestPerformRequest_UnparseableContentTypeWarns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header()["Content-Type"] = []string{"application/json; charset="}
		_, _ = w.Write([]byte(`{"advertisement":"probe"}`))
	}))
	defer srv.Close()

	logs := captureLogs(t)

	prober := NewTollGateProber(&config_manager.UpstreamDetectorConfig{}).(*tollGateProber)
	if _, err := prober.performRequestWithContext(context.Background(), srv.URL); err != nil {
		t.Fatalf("performRequestWithContext: %v", err)
	}

	if got := logs.String(); !strings.Contains(got, "unexpected content type") {
		t.Errorf("prober did not warn on an unparseable Content-Type, want a warning; log output:\n%s", got)
	}
}
