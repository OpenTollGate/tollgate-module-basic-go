package cli

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	wgm "github.com/OpenTollGate/tollgate-module-basic-go/src/wireless_gateway_manager"
	"github.com/stretchr/testify/require"
)

// The connector runs its actual UCI parsing and profile-writing methods. Only
// radio/WWAN setup and association are replaced; no hardware command is run.
type profileConnector struct {
	wgm.Connector
	setups int
}

func (*profileConnector) EnsureRadiosEnabled() error { return nil }
func (c *profileConnector) EnsureWWANSetup() error   { c.setups++; return nil }
func (c *profileConnector) SwitchUpstream(old, next, ssid, bssid string) error {
	if old != "" && old != next {
		if _, e := c.ExecuteUCI("set", "wireless."+old+".disabled=1"); e != nil {
			return e
		}
	}
	_, e := c.ExecuteUCI("set", "wireless."+next+".disabled=0")
	return e
}

func TestSavedProfileIntegrationSelectedAPSecurity(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, compatible := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%v/compatible=%v", stream, compatible), func(t *testing.T) {
				c, read := profileFixture(t, "psk2", false)
				before := read()
				selected := "none"
				other := "psk2"
				if compatible {
					selected, other = "psk2", "none"
				}
				networks := []wgm.NetworkInfo{
					{SSID: "Saved", Radio: "radio1", BSSID: "02:00:00:00:00:02", Signal: -20, Encryption: other},
					{SSID: "Saved", Radio: "radio0", BSSID: "02:00:00:00:00:03", Signal: -70, Encryption: other},
					{SSID: "Saved", Radio: "radio0", BSSID: "02:00:00:00:00:01", Signal: -30, Encryption: selected},
				}
				sc := &MockCLIScanner{}
				sc.On("ScanAllRadios").Return(networks, nil)
				sc.On("FindBestRadioForSSID", "Saved", networks).Return("radio0", nil)
				sc.On("DetectEncryption", selected).Return(selected)
				response := profileConnect(t, newTestCLIServer(c, sc), stream)
				require.Equal(t, compatible, response.Success)
				if !compatible {
					after := read()
					if c.setups != 0 || len(after.Writes) != 0 || !reflect.DeepEqual(before.Values, after.Values) {
						t.Error("untrusted selected AP changed the saved profile")
					}
				}
				sc.AssertExpectations(t)
			})
		}
	}
}

func TestSavedProfileIntegrationOpenControl(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint("stream=", stream), func(t *testing.T) {
			c, read := profileFixture(t, "none", false)
			require.True(t, profileConnect(t, newTestCLIServer(c, profileScanner("none")), stream).Success)
			if _, ok := read().Values["wireless.saved.key"]; ok {
				t.Error("open profile retained a stale key")
			}
		})
	}
}

type profileUCIState struct {
	Values map[string]string
	Writes []string
}

// This synthetic subprocess is the uci executable, not a connector mock. It
// records operation/field names only, never credential-bearing arguments.
func TestSavedProfileUCIProcess(t *testing.T) {
	if os.Getenv("TG_PROFILE_UCI_CHILD") != "1" {
		return
	}
	split := -1
	for i, a := range os.Args {
		if a == "--" {
			split = i
			break
		}
	}
	if split < 0 {
		os.Exit(2)
	}
	args := os.Args[split+1:]
	path := os.Getenv("TG_PROFILE_UCI_STATE")
	b, e := os.ReadFile(path)
	if e != nil {
		os.Exit(2)
	}
	var st profileUCIState
	if json.Unmarshal(b, &st) != nil {
		os.Exit(2)
	}
	if len(args) < 2 {
		os.Exit(2)
	}
	switch args[0] {
	case "show":
		keys := make([]string, 0, len(st.Values))
		for k := range st.Values {
			if strings.HasPrefix(k, args[1]+".") {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			if strings.Count(k, ".") == 1 {
				fmt.Printf("%s=%s\n", k, st.Values[k])
			} else {
				fmt.Printf("%s='%s'\n", k, st.Values[k])
			}
		}
	case "get":
		value, ok := st.Values[args[1]]
		if !ok {
			os.Exit(1)
		}
		fmt.Println(value)
	case "set":
		pair := strings.SplitN(args[1], "=", 2)
		if len(pair) != 2 {
			os.Exit(2)
		}
		st.Values[pair[0]] = pair[1]
		st.Writes = append(st.Writes, "set "+pair[0])
	case "delete":
		delete(st.Values, args[1])
		st.Writes = append(st.Writes, "delete "+args[1])
	case "commit":
		st.Writes = append(st.Writes, "commit "+args[1])
	default:
		os.Exit(2)
	}
	if len(st.Writes) > 0 {
		out, e := json.Marshal(st)
		if e != nil || os.WriteFile(path, out, 0600) != nil {
			os.Exit(2)
		}
	}
	os.Exit(0)
}

func profileFixture(t *testing.T, encryption string, active bool) (*profileConnector, func() profileUCIState) {
	t.Helper()
	dir := t.TempDir()
	exe, e := os.Executable()
	require.NoError(t, e)
	statePath := filepath.Join(dir, "state.json")
	st := profileUCIState{Values: map[string]string{
		"wireless.saved": "wifi-iface", "wireless.saved.mode": "sta",
		"wireless.saved.ssid": "Saved", "wireless.saved.device": "radio0",
		"wireless.saved.encryption": encryption, "wireless.saved.network": "wwan",
		"wireless.saved.key": "synthetic-fixture-key", "wireless.saved.disabled": "1",
	}}
	if active {
		st.Values["wireless.saved.disabled"] = "0"
	}
	b, e := json.Marshal(st)
	require.NoError(t, e)
	require.NoError(t, os.WriteFile(statePath, b, 0600))
	shim := "#!/bin/sh\nexec \"$TG_PROFILE_UCI_EXE\" -test.run=^TestSavedProfileUCIProcess$ -- \"$@\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "uci"), []byte(shim), 0700))
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	t.Setenv("TG_PROFILE_UCI_CHILD", "1")
	t.Setenv("TG_PROFILE_UCI_EXE", exe)
	t.Setenv("TG_PROFILE_UCI_STATE", statePath)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	read := func() profileUCIState {
		t.Helper()
		b, e := os.ReadFile(statePath)
		require.NoError(t, e)
		var v profileUCIState
		require.NoError(t, json.Unmarshal(b, &v))
		return v
	}
	return &profileConnector{}, read
}

func profileConnect(t *testing.T, s *CLIServer, streaming bool) CLIResponse {
	t.Helper()
	if !streaming {
		return s.handleUpstreamConnect([]string{"Saved"})
	}
	server, client := net.Pipe()
	defer client.Close()
	require.NoError(t, client.SetReadDeadline(time.Now().Add(5*time.Second)))
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer server.Close()
		s.handleUpstreamConnectStreaming(server, CLIMessage{Command: "upstream", Args: []string{"connect", "Saved"}})
	}()
	dec := json.NewDecoder(client)
	for {
		var raw json.RawMessage
		require.NoError(t, dec.Decode(&raw))
		if strings.Contains(string(raw), "synthetic-fixture-key") {
			t.Error("stream exposed a synthetic credential")
		}
		var response CLIResponse
		require.NoError(t, json.Unmarshal(raw, &response))
		if response.Progress == "" {
			client.Close() // release the writer’s separate trailing-newline write
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("stream handler did not finish")
			}
			return response
		}
	}
}

func profileScanner(encryption string) *MockCLIScanner {
	sc := &MockCLIScanner{}
	networks := []wgm.NetworkInfo{{SSID: "Saved", Radio: "radio0", BSSID: "02:00:00:00:00:01", Encryption: encryption}}
	sc.On("ScanAllRadios").Return(networks, nil)
	sc.On("FindBestRadioForSSID", "Saved", networks).Return("radio0", nil)
	sc.On("DetectEncryption", encryption).Return(encryption)
	return sc
}

func TestSavedProfileIntegrationActiveIsExcluded(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint("stream=", stream), func(t *testing.T) {
			c, read := profileFixture(t, "psk2", true)
			before := read()
			s := newTestCLIServer(c, profileScanner("psk2"))
			list := s.handleUpstreamList()
			b, e := json.Marshal(list)
			require.NoError(t, e)
			if strings.Contains(string(b), "\"can_reconnect\":true") {
				t.Error("active profile advertises unsupported credential-free reconnect")
			}
			for i := 0; i < 2; i++ {
				if profileConnect(t, s, stream).Success {
					t.Error("active profile reconnect must be refused before profile mutation")
				}
			}
			after := read()
			if c.setups != 0 || len(after.Writes) != 0 || !reflect.DeepEqual(before.Values, after.Values) {
				t.Error("active profile reconnect mutated profiles or created a duplicate")
			}
			sections, e := c.GetSTASections()
			require.NoError(t, e)
			require.Len(t, sections, 1)
		})
	}
}

func TestSavedProfileIntegrationSecurityBinding(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, scan := range []string{"none", "sae", "psk", "psk-mixed", "wpa2"} {
			t.Run(fmt.Sprintf("stream=%v/scan=%s", stream, scan), func(t *testing.T) {
				c, read := profileFixture(t, "psk2", false)
				before := read()
				s := newTestCLIServer(c, profileScanner(scan))
				if profileConnect(t, s, stream).Success {
					t.Error("incompatible advertisement accepted stored credentials")
				}
				after := read()
				if c.setups != 0 || len(after.Writes) != 0 || !reflect.DeepEqual(before.Values, after.Values) {
					t.Error("incompatible scan changed saved security or UCI state")
				}
			})
		}
	}
}

func TestSavedProfileIntegrationCompatibleCipherPreserved(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint("stream=", stream), func(t *testing.T) {
			c, read := profileFixture(t, "psk2+ccmp", false)
			s := newTestCLIServer(c, profileScanner("psk2"))
			require.True(t, profileConnect(t, s, stream).Success)
			st := read()
			if st.Values["wireless.saved.encryption"] != "psk2+ccmp" {
				t.Error("saved cipher settings were overwritten")
			}
			if st.Values["wireless.saved.key"] != "synthetic-fixture-key" {
				t.Error("saved key was changed")
			}
			if profileConnect(t, s, stream).Success {
				t.Error("now-active profile must be excluded on a repeated attempt")
			}
			sections, e := c.GetSTASections()
			require.NoError(t, e)
			require.Len(t, sections, 1)
		})
	}
}
