package main

import (
	"crypto/tls"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDefaultTransport_TLSMaxVersionIsTLS12(t *testing.T) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	assert.True(t, ok, "DefaultTransport should be *http.Transport")

	assert.NotNil(t, transport.TLSClientConfig, "TLSClientConfig should be set")
	assert.Equal(t, uint16(tls.VersionTLS12), transport.TLSClientConfig.MaxVersion,
		"TLS MaxVersion must be TLS 1.2 for OpenWrt compatibility")
	assert.Equal(t, uint16(tls.VersionTLS12), transport.TLSClientConfig.MinVersion,
		"TLS MinVersion must be TLS 1.2")
}

func TestDefaultTransport_TimeoutsSet(t *testing.T) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	assert.True(t, ok)

	assert.Equal(t, 30*time.Second, transport.ResponseHeaderTimeout)
	assert.Equal(t, 20*time.Second, transport.TLSHandshakeTimeout)
	assert.Equal(t, 30*time.Second, transport.IdleConnTimeout)
}

func TestDefaultTransport_HTTP2Disabled(t *testing.T) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	assert.True(t, ok)
	assert.False(t, transport.ForceAttemptHTTP2)
}

func TestDefaultTransport_DisableKeepAlives(t *testing.T) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	assert.True(t, ok)
	assert.True(t, transport.DisableKeepAlives)
}

func TestDefaultClient_TimeoutSet(t *testing.T) {
	assert.Equal(t, 30*time.Second, http.DefaultClient.Timeout)
}
