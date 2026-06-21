package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestE2E_HandleDetails(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	handleDetails(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "kind")
}

func TestE2E_HandleRootPost_InvalidBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	w := httptest.NewRecorder()

	HandleRootPost(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestE2E_HandleBalance_MACLookupFails(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/balance", nil)
	req.RemoteAddr = "192.168.1.1:1234"
	w := httptest.NewRecorder()

	HandleBalance(w, req)

	assert.True(t, w.Code == http.StatusBadRequest || w.Code == http.StatusOK)
}

func TestE2E_LightningInvoice_MissingParams(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/ln-invoice", strings.NewReader(`{"mint_url": "https://test.com"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.168.1.1:1234"
	w := httptest.NewRecorder()

	handleLightningInvoicePost(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp lightningInvoiceResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Contains(t, resp.Error, "amount")
}

func TestE2E_LightningInvoiceGet_MissingQuote(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ln-invoice", nil)
	req.RemoteAddr = "192.168.1.1:1234"
	w := httptest.NewRecorder()

	handleLightningInvoiceGet(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp lightningInvoiceResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, "quote is required", resp.Error)
}

func TestE2E_HandleRoot_RoutesGet(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	HandleRoot(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestE2E_HandleRoot_RoutesPost(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	w := httptest.NewRecorder()

	HandleRoot(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
