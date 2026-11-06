package cli

import (
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"

	wgm "github.com/OpenTollGate/tollgate-module-basic-go/src/wireless_gateway_manager"
	"github.com/stretchr/testify/require"
)

func TestSavedUpstreamCredentialEligibility(t *testing.T) {
	for _, tc := range []struct {
		name, encryption, network, key string
		duplicate, readError, wantOK   bool
	}{
		{name: "personal", encryption: "psk2+ccmp", network: "wwan", key: " fixture password ", wantOK: true},
		{name: "open", encryption: "none", network: "wwan", wantOK: true},
		{name: "missing key", encryption: "sae", network: "wwan"},
		{name: "other network", encryption: "psk2", network: "lan", key: "fixture-password"},
		{name: "ambiguous", encryption: "psk2", duplicate: true},
		{name: "enterprise", encryption: "wpa2"},
		{name: "read failure", encryption: "psk2", network: "wwan", readError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &MockCLIConnector{}
			sections := []wgm.STASection{{Name: "saved", SSID: "Saved", Encryption: tc.encryption}}
			if tc.duplicate {
				sections = append(sections, sections[0])
			}
			if !tc.duplicate && tc.encryption != "wpa2" {
				c.On("ExecuteUCI", "get", "wireless.saved.network").Return(tc.network+"\n", nil)
				if tc.network == "wwan" && tc.encryption != "none" {
					var err error
					if tc.readError {
						err = errors.New("fixture-password must not escape")
					}
					c.On("ExecuteUCI", "get", "wireless.saved.key").Return(tc.key+"\n", err)
				}
			}
			key, err := newTestCLIServer(c, &MockCLIScanner{}).savedUpstreamPassphrase("Saved", sections)
			if tc.wantOK {
				require.NoError(t, err)
				require.Equal(t, tc.key, key)
			} else {
				require.Error(t, err)
				require.Empty(t, key)
				require.NotContains(t, err.Error(), "fixture-password")
			}
			c.AssertExpectations(t)
		})
	}
}

func TestSavedUpstreamConnectBothHandlers(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, explicit := range []bool{false, true} {
			for _, fail := range []bool{false, true} {
				t.Run(strings.Join([]string{map[bool]string{false: "normal", true: "streaming"}[streaming], map[bool]string{false: "saved", true: "explicit"}[explicit], map[bool]string{false: "ok", true: "failure"}[fail]}, "/"), func(t *testing.T) {
					c, sc := &MockCLIConnector{}, &MockCLIScanner{}
					networks := []wgm.NetworkInfo{{SSID: "Saved", Radio: "radio0", Encryption: "WPA2"}}
					c.On("EnsureRadiosEnabled").Return(nil)
					sc.On("ScanAllRadios").Return(networks, nil)
					sc.On("FindBestRadioForSSID", "Saved", networks).Return("radio0", nil)
					sc.On("DetectEncryption", "WPA2").Return("psk2")
					args, key := []string{"Saved"}, "stored-fixture-password"
					if explicit {
						key = "entered-fixture-password"
						args = append(args, key)
					} else {
						c.On("GetSTASections").Return([]wgm.STASection{{Name: "saved", SSID: "Saved", Encryption: "psk2", Disabled: true}}, nil)
						c.On("ExecuteUCI", "get", "wireless.saved.network").Return("wwan\n", nil)
						c.On("ExecuteUCI", "get", "wireless.saved.key").Return(key+"\n", nil)
					}
					c.On("EnsureWWANSetup").Return(nil)
					var createErr error
					if fail {
						createErr = errors.New(key)
					}
					c.On("FindOrCreateSTAForSSID", "Saved", key, "psk2", "radio0").Return("saved", createErr)
					if !fail {
						c.On("GetActiveSTA").Return(nil, nil)
						c.On("SwitchUpstream", "", "saved", "Saved", "").Return(nil)
					}
					s := newTestCLIServer(c, sc)
					var response CLIResponse
					if !streaming {
						response = s.handleUpstreamConnect(args)
					} else {
						server, client := net.Pipe()
						done := make(chan struct{})
						go func() {
							defer close(done)
							defer server.Close()
							s.handleUpstreamConnectStreaming(server, CLIMessage{Command: "upstream", Args: append([]string{"connect"}, args...)})
						}()
						decoder := json.NewDecoder(client)
						for {
							response = CLIResponse{}
							require.NoError(t, decoder.Decode(&response))
							if response.Progress == "" {
								break
							}
						}
						client.Close()
						<-done
					}
					require.Equal(t, !fail, response.Success)
					data, err := json.Marshal(response)
					require.NoError(t, err)
					require.NotContains(t, string(data), key)
					c.AssertExpectations(t)
					sc.AssertExpectations(t)
				})
			}
		}
	}
}

func TestSavedUpstreamListExposesCapabilityWithoutCredential(t *testing.T) {
	c := &MockCLIConnector{}
	c.On("GetSTASections").Return([]wgm.STASection{{Name: "saved", SSID: "Saved", Encryption: "psk2"}}, nil)
	c.On("ExecuteUCI", "get", "wireless.saved.network").Return("wwan\n", nil)
	c.On("ExecuteUCI", "get", "wireless.saved.key").Return("stored-fixture-password\n", nil)
	response := newTestCLIServer(c, &MockCLIScanner{}).handleUpstreamList()
	require.True(t, response.Success)
	data, err := json.Marshal(response)
	require.NoError(t, err)
	require.Contains(t, string(data), `"can_reconnect":true`)
	require.NotContains(t, string(data), "stored-fixture-password")
}
