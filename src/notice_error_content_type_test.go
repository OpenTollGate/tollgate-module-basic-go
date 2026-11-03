package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/merchant"
	"github.com/nbd-wtf/go-nostr"
)

// failingNoticeMerchant is the shared namedMerchant stub with the single
// method sendNoticeResponse's error path depends on made to fail.
type failingNoticeMerchant struct {
	namedMerchant
}

func (m *failingNoticeMerchant) CreateNoticeEvent(level, code, message, customerPubkey string) (*nostr.Event, error) {
	return nil, errors.New("notice creation failed")
}

// noticeOverRealServer serves one sendNoticeResponse call over a real TCP
// listener and returns the client-side response.
//
// A httptest.ResponseRecorder cannot prove this defect either way: it records
// Header() as-written and never runs net/http's content sniffer, so both the
// broken and the fixed code return an empty Content-Type there. The sniffing
// only happens in the server's writeHeader path, which is why the finding was
// measured against a live server (#628) and why this probe uses one too.
func noticeOverRealServer(t *testing.T, m merchant.MerchantInterface) *http.Response {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sendNoticeResponse(w, m, http.StatusBadRequest, "error", "probe", "message", "")
	}))
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("GET %s: %v", srv.URL, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// #628, :2121 half. sendNoticeResponse encodes a JSON body on BOTH paths, so
// both must label it. The success path set Content-Type; the error path did
// not, and the live server content-sniffed the JSON error object into
// "text/plain; charset=utf-8". Upstream clients read the label, so a
// can't-create-a-notice condition surfaced as an unparseable plain-text body.
func TestSendNoticeResponseErrorPathSetsJSONContentType(t *testing.T) {
	resp := noticeOverRealServer(t, &failingNoticeMerchant{})

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("error path Content-Type = %q, want %q — the body is JSON and must be labelled as such (#628)",
			got, "application/json")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), `"error"`) {
		t.Errorf("body = %q, want the JSON error object", body)
	}
}

// A can't-fail assertion is decoration. The success path already carried the
// header, so this pins it: if the label were moved to the wrong branch, or
// dropped from both, the error-path test alone would not notice.
func TestSendNoticeResponseSuccessPathSetsJSONContentType(t *testing.T) {
	resp := noticeOverRealServer(t, &boundaryMerchant{rec: &noticeRecorder{}})

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("success path Content-Type = %q, want %q", got, "application/json")
	}
}
