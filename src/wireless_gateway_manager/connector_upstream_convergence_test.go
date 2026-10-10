package wireless_gateway_manager

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// fakeUCI is an in-memory uci good enough for the connector's STA logic:
// section-type lines render unquoted (exactly how `uci show wireless`
// renders them, which is what GetSTASections' parser depends on), `get`
// returns the stored value or the real uci's "Entry not found" error, and
// every invocation is recorded so tests assert the write sequence, not
// just the end state.
type fakeUCI struct {
	mu    sync.Mutex
	state map[string]string
	calls []string
}

func newFakeUCI() *fakeUCI {
	return &fakeUCI{state: map[string]string{}}
}

func (f *fakeUCI) set(key, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state[key] = value
}

func (f *fakeUCI) get(key string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.state[key]
	return v, ok
}

func (f *fakeUCI) callLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeUCI) saw(cmd string) bool {
	for _, c := range f.callLog() {
		if c == cmd {
			return true
		}
	}
	return false
}

func (f *fakeUCI) exec(args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.Join(args, " "))

	switch args[0] {
	case "show":
		if args[1] != "wireless" {
			return "", fmt.Errorf("fake uci: unsupported show target %s", args[1])
		}
		keys := make([]string, 0, len(f.state))
		for k := range f.state {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		for _, k := range keys {
			if len(strings.Split(k, ".")) == 2 {
				// Section-type line, unquoted — the shape GetSTASections'
				// parser keys on.
				b.WriteString(k + "=" + f.state[k] + "\n")
			} else {
				b.WriteString(k + "='" + f.state[k] + "'\n")
			}
		}
		return b.String(), nil
	case "get":
		v, ok := f.state[args[1]]
		if !ok {
			return "", fmt.Errorf("uci: Entry not found")
		}
		return v + "\n", nil
	case "set":
		parts := strings.SplitN(args[1], "=", 2)
		f.state[parts[0]] = parts[1]
		return "", nil
	case "delete":
		delete(f.state, args[1])
		return "", nil
	case "commit":
		return "", nil
	}
	return "", fmt.Errorf("fake uci: unsupported command %v", args)
}

// seedSTA writes one STA section into the fake in the shape GetSTASections
// parses back out.
func seedSTA(f *fakeUCI, name, ssid, device, encryption, key, disabled string) {
	f.set("wireless."+name, "wifi-iface")
	f.set("wireless."+name+".mode", "sta")
	f.set("wireless."+name+".ssid", ssid)
	f.set("wireless."+name+".device", device)
	if encryption != "" {
		f.set("wireless."+name+".encryption", encryption)
	}
	if key != "" {
		f.set("wireless."+name+".key", key)
	}
	if disabled != "" {
		f.set("wireless."+name+".disabled", disabled)
	}
}

// #817: reconnecting to a known SSID whose passphrase rotated must
// overwrite the stored key on the reused section — the old code returned
// the section with whatever key it was created with, and the join failed
// WRONG_KEY while the connect flow reported a DHCP wait.
func TestFindOrCreateSTAForSSID_ReuseRefreshesCredentials(t *testing.T) {
	f := newFakeUCI()
	seedSTA(f, "upstream_testnet", "TestNet", "radio0", "psk2", "old-password", "1")

	c := &Connector{runUCI: f.exec}
	iface, err := c.FindOrCreateSTAForSSID("TestNet", "rotated-password", "psk2", "radio0")

	assert.NoError(t, err)
	assert.Equal(t, "upstream_testnet", iface, "must reuse the existing disabled section, not create one")

	key, ok := f.get("wireless.upstream_testnet.key")
	assert.True(t, ok, "the reused section must carry a key")
	assert.Equal(t, "rotated-password", key, "the stored key must be the passphrase just given, not the stale one")
	assert.Equal(t, "psk2", mustGet(t, f, "wireless.upstream_testnet.encryption"))
	assert.Equal(t, "wwan", mustGet(t, f, "wireless.upstream_testnet.network"))
	assert.True(t, f.saw("commit wireless"), "the credential refresh must be committed")
	assert.False(t, f.saw("set wireless.upstream_testnet_testnet=wifi-iface"),
		"no new section may be created when a reusable one exists")
}

// The reverse transition: a section created encrypted, later reused for an
// open network, must not keep a stale key (a leftover key on an open
// network is at best ignored, at worst a join failure).
func TestFindOrCreateSTAForSSID_ReuseOpenNetworkDropsStaleKey(t *testing.T) {
	f := newFakeUCI()
	seedSTA(f, "upstream_cafe", "Cafe", "radio0", "psk2", "old-password", "1")

	c := &Connector{runUCI: f.exec}
	iface, err := c.FindOrCreateSTAForSSID("Cafe", "", "none", "radio0")

	assert.NoError(t, err)
	assert.Equal(t, "upstream_cafe", iface)
	assert.Equal(t, "none", mustGet(t, f, "wireless.upstream_cafe.encryption"))
	_, hasKey := f.get("wireless.upstream_cafe.key")
	assert.False(t, hasKey, "reusing the section for an open network must delete the stale key")
}

// #817, the desk-run shape: a foreign enabled STA section on the target
// radio (there: `tollgate_uplink` with a garbage key) keeps the radio's
// single STA netdev, so the stale key is what wpa_supplicant joins with
// while the freshly written candidate never gets an interface. The switch
// must disable every other enabled STA on the candidate's radio.
func TestSwitchUpstream_DisablesCompetingSTAOnTargetRadio(t *testing.T) {
	f := newFakeUCI()
	seedSTA(f, "upstream_newnet", "NewNet", "radio0", "psk2", "new-password", "1")
	seedSTA(f, "tollgate_uplink", "Foreign", "radio0", "psk2", "garbage", "0")
	seedSTA(f, "upstream_oldnet", "OldNet", "radio1", "psk2", "old-password", "0")

	// DHCPTimeout of ~0 makes waitForSTAIP return immediately (the fake has
	// no netdevs to find), so the switch takes its documented revert path.
	c := &Connector{runUCI: f.exec, DHCPTimeout: time.Millisecond}
	err := c.SwitchUpstream("upstream_oldnet", "upstream_newnet", "NewNet")

	assert.Error(t, err, "with no joinable netdev the switch reports its DHCP-timeout revert")
	assert.Equal(t, "1", mustGet(t, f, "wireless.tollgate_uplink.disabled"),
		"the competing enabled STA on the target radio must be disabled — and the revert path must not resurrect it")
	assert.Equal(t, "1", mustGet(t, f, "wireless.upstream_newnet.disabled"),
		"the candidate ends disabled again: the documented DHCP-timeout revert")
	assert.Equal(t, "0", mustGet(t, f, "wireless.upstream_oldnet.disabled"),
		"the revert restores the previous active upstream (other radio)")
	assert.True(t, f.saw("set wireless.upstream_newnet.disabled=0"),
		"the candidate is enabled before the wait")
}

func mustGet(t *testing.T, f *fakeUCI, key string) string {
	t.Helper()
	v, ok := f.get(key)
	if !ok {
		t.Fatalf("fake uci has no %s", key)
	}
	return v
}
