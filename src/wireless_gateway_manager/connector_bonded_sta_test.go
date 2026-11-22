package wireless_gateway_manager

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsBondedSTA(t *testing.T) {
	c := &Connector{BondedSTANames: map[string]bool{"hotel_sta": true}}
	require.True(t, c.isBondedSTA("hotel_sta"))
	require.True(t, c.isBondedSTA("wireless.hotel_sta"))
	require.False(t, c.isBondedSTA("tollgate_sta_2g"))
	require.False(t, c.isBondedSTA("wireless.other"))
}

func TestDisableOtherSTAInterfacesSkipsBondedMembers(t *testing.T) {
	var commands []string
	c := &Connector{
		BondedSTANames: map[string]bool{"hotel_sta": true},
		runUCI: func(args ...string) (string, error) {
			commands = append(commands, strings.Join(args, " "))
			if len(args) == 2 && args[0] == "show" && args[1] == "wireless" {
				return "wireless.hotel_sta.mode='sta'\nwireless.other_sta.mode='sta'\n", nil
			}
			return "", nil
		},
	}

	require.NoError(t, c.disableOtherSTAInterfaces("wireless.active_sta"))
	joined := strings.Join(commands, "\n")
	require.NotContains(t, joined, "hotel_sta.disabled=1")
	require.Contains(t, joined, "other_sta.disabled=1")
}
