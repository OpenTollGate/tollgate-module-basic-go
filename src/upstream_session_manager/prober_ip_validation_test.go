package upstream_session_manager

import (
	"context"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/stretchr/testify/assert"
)

func newTestProber() TollGateProber {
	return NewTollGateProber(&config_manager.UpstreamDetectorConfig{})
}

func TestProbeGateway_RejectsLoopback(t *testing.T) {
	prober := newTestProber()
	_, err := prober.ProbeGatewayWithContext(context.Background(), "test", "127.0.0.1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "loopback")
}

func TestProbeGateway_RejectsLoopbackIPv6(t *testing.T) {
	prober := newTestProber()
	_, err := prober.ProbeGatewayWithContext(context.Background(), "test", "::1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "loopback")
}

func TestProbeGateway_RejectsUnspecified(t *testing.T) {
	prober := newTestProber()
	_, err := prober.ProbeGatewayWithContext(context.Background(), "test", "0.0.0.0")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unspecified")
}

func TestProbeGateway_RejectsLinkLocal(t *testing.T) {
	prober := newTestProber()
	_, err := prober.ProbeGatewayWithContext(context.Background(), "test", "169.254.1.1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "link-local")
}

func TestProbeGateway_RejectsEmpty(t *testing.T) {
	prober := newTestProber()
	_, err := prober.ProbeGatewayWithContext(context.Background(), "test", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestProbeGateway_RejectsInvalidIP(t *testing.T) {
	prober := newTestProber()
	_, err := prober.ProbeGatewayWithContext(context.Background(), "test", "not-an-ip")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid gateway IP")
}

func TestProbeGateway_AcceptsPrivateIP(t *testing.T) {
	prober := newTestProber()
	_, err := prober.ProbeGatewayWithContext(context.Background(), "test", "192.168.1.1")
	assert.Error(t, err)
	assert.NotContains(t, err.Error(), "loopback")
	assert.NotContains(t, err.Error(), "link-local")
}

func TestTriggerCaptivePortal_RejectsLoopback(t *testing.T) {
	prober := newTestProber()
	err := prober.TriggerCaptivePortalSession(context.Background(), "127.0.0.1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "loopback")
}

func TestTriggerCaptivePortal_RejectsLinkLocal(t *testing.T) {
	prober := newTestProber()
	err := prober.TriggerCaptivePortalSession(context.Background(), "169.254.169.254")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "link-local")
}

func TestTriggerCaptivePortal_RejectsEmpty(t *testing.T) {
	prober := newTestProber()
	err := prober.TriggerCaptivePortalSession(context.Background(), "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}
