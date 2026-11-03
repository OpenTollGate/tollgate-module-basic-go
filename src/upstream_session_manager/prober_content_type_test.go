package upstream_session_manager

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/sirupsen/logrus"
)

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

	var logs bytes.Buffer
	prevOut := logrus.StandardLogger().Out
	prevLevel := logrus.GetLevel()
	logrus.SetOutput(&logs)
	logrus.SetLevel(logrus.DebugLevel)
	defer func() {
		logrus.SetOutput(prevOut)
		logrus.SetLevel(prevLevel)
	}()

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

	var logs bytes.Buffer
	prevOut := logrus.StandardLogger().Out
	prevLevel := logrus.GetLevel()
	logrus.SetOutput(&logs)
	logrus.SetLevel(logrus.DebugLevel)
	defer func() {
		logrus.SetOutput(prevOut)
		logrus.SetLevel(prevLevel)
	}()

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

	var logs bytes.Buffer
	prevOut := logrus.StandardLogger().Out
	prevLevel := logrus.GetLevel()
	logrus.SetOutput(&logs)
	logrus.SetLevel(logrus.DebugLevel)
	defer func() {
		logrus.SetOutput(prevOut)
		logrus.SetLevel(prevLevel)
	}()

	prober := NewTollGateProber(&config_manager.UpstreamDetectorConfig{}).(*tollGateProber)
	if _, err := prober.performRequestWithContext(context.Background(), srv.URL); err != nil {
		t.Fatalf("performRequestWithContext: %v", err)
	}

	if got := logs.String(); !strings.Contains(got, "unexpected content type") {
		t.Errorf("prober did not warn on an unparseable Content-Type, want a warning; log output:\n%s", got)
	}
}
