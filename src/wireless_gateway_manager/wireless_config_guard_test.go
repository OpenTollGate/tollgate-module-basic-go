package wireless_gateway_manager

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetRadios_MissingWirelessConfig(t *testing.T) {
	s := &Scanner{Connector: &Connector{}}
	radios, err := s.GetRadios()
	assert.NoError(t, err, "missing /etc/config/wireless should not error")
	assert.Nil(t, radios, "missing file should return nil radios")
}

func TestGetRadiosFromConfig_MissingWirelessConfig(t *testing.T) {
	c := &Connector{}
	radios, err := c.getRadiosFromConfig()
	assert.NoError(t, err, "missing /etc/config/wireless should not error")
	assert.Nil(t, radios, "missing file should return nil radios")
}
