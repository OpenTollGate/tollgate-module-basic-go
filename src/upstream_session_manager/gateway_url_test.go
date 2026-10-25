package upstream_session_manager

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGatewayURLIPv4Unchanged(t *testing.T) {
	assert.Equal(t, "http://192.168.2.32:2121/", gatewayURL("192.168.2.32", "2121", ""))
}

func TestGatewayURLIPv6Bracketed(t *testing.T) {
	assert.Equal(t, "http://[fd42::1]:2121/", gatewayURL("fd42::1", "2121", ""))
}

func TestGatewayURLIPv6WithPath(t *testing.T) {
	assert.Equal(t, "http://[fd42::1]:2121/usage", gatewayURL("fd42::1", "2121", "usage"))
}

func TestGatewayURLHostnameUnchanged(t *testing.T) {
	assert.Equal(t, "http://gw.example:2121/", gatewayURL("gw.example", "2121", ""))
}
