package merchant

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

// TestOpenServiceWalletSelection (tbmg#1139): wallet.backend selects the
// service's Cashu wallet through the tollwallet.OpenWallet seam. The
// absent-section case must keep the pre-selection behavior exactly (the
// in-process wallet), and "cdk" must open the sidecar client — lazy, so
// the type is observable without a running daemon.
func TestOpenServiceWalletSelection(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	installPath := filepath.Join(dir, "install.json")
	identitiesPath := filepath.Join(dir, "identities.json")
	if err := os.WriteFile(configPath, []byte(`{"config_version":"v0.0.10","log_level":"info","accepted_mints":[{"url":"https://mint.example/","min_balance":64,"balance_tolerance_percent":10,"payout_interval_seconds":60,"min_payout_amount":128,"price_per_step":1,"price_unit":"sat","purchase_min_steps":1}],"step_size":22020096,"metric":"bytes","show_setup":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	mints := []string{"https://mint.example/"}
	writeConfig := func(t *testing.T, extra string) *config_manager.ConfigManager {
		t.Helper()
		body := `{"config_version":"v0.0.10","log_level":"info","accepted_mints":[{"url":"https://mint.example/","min_balance":64,"balance_tolerance_percent":10,"payout_interval_seconds":60,"min_payout_amount":128,"price_per_step":1,"price_unit":"sat","purchase_min_steps":1}],"step_size":22020096,"metric":"bytes","show_setup":true`
		if extra != "" {
			body += "," + extra
		}
		body += "}"
		if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		cm, err := config_manager.NewConfigManager(configPath, installPath, identitiesPath)
		if err != nil {
			t.Fatal(err)
		}
		return cm
	}

	t.Run("absent_section_selects_in_process", func(t *testing.T) {
		cm := writeConfig(t, "")
		if cm.GetConfig().Wallet.Backend != "" {
			t.Fatalf("legacy config must parse with an empty wallet section, got %+v", cm.GetConfig().Wallet)
		}
		wp, err := openServiceWallet(cm, dir, mints)
		if err != nil {
			t.Fatalf("openServiceWallet: %v", err)
		}
		if _, ok := wp.(*tollwallet.GonutsWallet); !ok {
			t.Fatalf("absent wallet section must select the in-process wallet, got %T", wp)
		}
	})

	t.Run("default_selects_in_process", func(t *testing.T) {
		cm := writeConfig(t, `"wallet":{"backend":"default","socket":"/var/run/cdk-walletd.sock"}`)
		wp, err := openServiceWallet(cm, dir, mints)
		if err != nil {
			t.Fatalf("openServiceWallet: %v", err)
		}
		if _, ok := wp.(*tollwallet.GonutsWallet); !ok {
			t.Fatalf("backend=default must select the in-process wallet, got %T", wp)
		}
	})

	t.Run("cdk_selects_the_sidecar_client", func(t *testing.T) {
		sock := filepath.Join(dir, "w.sock")
		// NewSidecarWallet runs the `info` handshake at construction —
		// a minimal fake daemon answers it (no real wallet needed to
		// observe the TYPE selection).
		ln, lerr := net.Listen("unix", sock)
		if lerr != nil {
			t.Fatal(lerr)
		}
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go func(c net.Conn) {
					defer c.Close()
					r := bufio.NewReader(c)
					for {
						line, err := r.ReadString('\n')
						if err != nil {
							return
						}
						var req struct {
							ID uint64 `json:"id"`
						}
						_ = json.Unmarshal([]byte(line), &req)
						resp, _ := json.Marshal(map[string]any{
							"id": req.ID, "ok": true,
							"result": map[string]any{
								"backend": "cdk", "kind": "sidecar",
								"arches": []string{"x86_64"},
							},
						})
						_, _ = c.Write(append(resp, '\n'))
					}
				}(c)
			}
		}()
		defer ln.Close()
		cm := writeConfig(t, `"wallet":{"backend":"cdk","socket":"`+sock+`"}`)
		wp, err := openServiceWallet(cm, dir, mints)
		if err != nil {
			t.Fatalf("openServiceWallet (cdk): %v", err)
		}
		if _, ok := wp.(*tollwallet.SidecarWallet); !ok {
			t.Fatalf("backend=cdk must select the sidecar client, got %T", wp)
		}
	})

	t.Run("cdk_without_socket_refuses", func(t *testing.T) {
		cm := writeConfig(t, `"wallet":{"backend":"cdk"}`)
		if _, err := openServiceWallet(cm, dir, mints); err == nil {
			t.Fatal("backend=cdk with no socket must fail loudly, not silently fall back")
		}
	})
}
