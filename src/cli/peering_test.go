package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type fakePeeringProvider struct{}

func (fakePeeringProvider) PeeringStatus() []PeeringSession {
	return []PeeringSession{{GatewayIP: "10.0.0.1", Status: "active", TotalSpentSats: 2}}
}

func TestHandleUpstreamStatusReturnsProviderData(t *testing.T) {
	s := &CLIServer{peeringProvider: fakePeeringProvider{}}
	resp := s.handleUpstreamStatus()
	assert.True(t, resp.Success)
	sessions, ok := resp.Data.([]PeeringSession)
	assert.True(t, ok)
	assert.Len(t, sessions, 1)
	assert.Equal(t, "10.0.0.1", sessions[0].GatewayIP)
}

func TestHandleUpstreamStatusWithoutProviderIsEmpty(t *testing.T) {
	s := &CLIServer{}
	resp := s.handleUpstreamStatus()
	assert.True(t, resp.Success)
	assert.Empty(t, resp.Data)
}
