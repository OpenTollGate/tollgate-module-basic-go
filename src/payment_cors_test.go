package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
)

func TestExtractCashuToken_RawToken(t *testing.T) {
	token, event := extractCashuToken([]byte("cashuB1234567890"))
	assert.Equal(t, "cashuB1234567890", token)
	assert.Nil(t, event)
}

func TestExtractCashuToken_RawToken_TrimsWhitespace(t *testing.T) {
	token, event := extractCashuToken([]byte("  cashuBxyz  \n"))
	assert.Equal(t, "cashuBxyz", token)
	assert.Nil(t, event)
}

func TestExtractCashuToken_Kind21000Wrapper(t *testing.T) {
	ev := nostr.Event{Kind: 21000, Tags: nostr.Tags{{"payment", "cashuBwrapped"}}}
	body, _ := json.Marshal(ev)

	token, parsedEvent := extractCashuToken(body)
	assert.Equal(t, "cashuBwrapped", token)
	assert.NotNil(t, parsedEvent)
	assert.Equal(t, 21000, parsedEvent.Kind)
}

func TestExtractCashuToken_Kind21000Wrapper_NoPaymentTag(t *testing.T) {
	ev := nostr.Event{Kind: 21000, Tags: nostr.Tags{{"other", "value"}}}
	body, _ := json.Marshal(ev)

	token, parsedEvent := extractCashuToken(body)
	assert.Equal(t, "", token)
	assert.NotNil(t, parsedEvent)
}

func TestExtractCashuToken_Non21000JSON(t *testing.T) {
	body, _ := json.Marshal(nostr.Event{Kind: 1})
	token, event := extractCashuToken(body)
	assert.NotNil(t, token)
	assert.Nil(t, event)
}

func TestCorsMiddleware_SetsCORSHeaders(t *testing.T) {
	handler := CorsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/usage", nil)
	req.Header.Set("Origin", "http://192.168.1.1:8080")
	handler(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "http://192.168.1.1:8080", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "GET, POST, OPTIONS", rec.Header().Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "Content-Type, Authorization", rec.Header().Get("Access-Control-Allow-Headers"))
}

func TestCorsMiddleware_PassesThroughNonOptions(t *testing.T) {
	called := false
	handler := CorsMiddleware(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler(rec, req)

	assert.True(t, called)
	assert.Equal(t, "GET, POST, OPTIONS", rec.Header().Get("Access-Control-Allow-Methods"))
}
