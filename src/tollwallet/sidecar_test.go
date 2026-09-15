package tollwallet

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"testing"
)

// fakeSidecar starts an in-process wallet daemon on a temp unix socket that
// answers the sidecar RPC using handler. It returns the socket path.
func fakeSidecar(t *testing.T, handler func(method string, params json.RawMessage) (any, error)) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "wallet.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				rd := bufio.NewReader(c)
				for {
					line, err := rd.ReadBytes('\n')
					if err != nil {
						return
					}
					var req sidecarRequest
					if json.Unmarshal(line, &req) != nil {
						return
					}
					res, herr := handler(req.Method, req.Params)
					resp := sidecarResponse{ID: req.ID, OK: herr == nil}
					if herr != nil {
						resp.Error = herr.Error()
					} else if res != nil {
						b, _ := json.Marshal(res)
						resp.Result = b
					}
					out, _ := json.Marshal(resp)
					_, _ = c.Write(append(out, '\n'))
				}
			}(conn)
		}
	}()
	return sock
}

func testHandler(method string, _ json.RawMessage) (any, error) {
	switch method {
	case "info":
		return Manifest{
			Backend: "fake", Kind: "sidecar", Version: "0.0.1",
			Arches:    []string{"aarch64_cortex-a53", "mipsel_24kc"},
			SizeBytes: map[string]int64{"aarch64_cortex-a53": 4_000_000},
			Storage:   StorageInfo{Model: "sqlite", WritesPerPayment: "o(1)", CrashConsistent: true},
		}, nil
	case "decode_token":
		return tokenJSON{Token: "cashuBfake", Mint: "https://mint.example", Amount: 21}, nil
	case "receive":
		return uint64(42), nil
	case "get_balance":
		return uint64(1000), nil
	case "get_balance_by_mint":
		return uint64(500), nil
	case "get_all_mint_balances":
		return map[string]uint64{"https://mint.example": 500}, nil
	case "send":
		return tokenJSON{Token: "cashuAfake", Mint: "https://mint.example", Amount: 5}, nil
	case "send_with_overpayment":
		return "cashuAovp", nil
	case "drain":
		return map[string]any{"token": "cashuAdrain", "mint": "https://mint.example", "amount": 500}, nil
	case "melt_to_lightning":
		return nil, nil
	case "request_mint_quote":
		return MintQuote{QuoteID: "q1", Request: "lnbc1", State: StateUnpaid, Amount: 21, Expiry: 123}, nil
	case "mint_quote_state":
		return StatePaid, nil
	case "mint_tokens":
		return uint64(21), nil
	case "request_melt_quote":
		return MeltQuote{QuoteID: "m1", Amount: 10, FeeReserve: 1, State: StateUnpaid, Expiry: 99}, nil
	case "melt":
		return MeltResult{QuoteID: "m1", Paid: true, Preimage: "ff"}, nil
	case "shutdown":
		return nil, nil
	default:
		return nil, errors.New("unknown method " + method)
	}
}

func TestSidecarWalletContract(t *testing.T) {
	sock := fakeSidecar(t, testHandler)

	sw, manifest, err := NewSidecarWallet(sock)
	if err != nil {
		t.Fatalf("NewSidecarWallet: %v", err)
	}
	if manifest.Backend != "fake" || manifest.Kind != "sidecar" {
		t.Errorf("unexpected manifest: %+v", manifest)
	}
	if manifest.SizeBytes["aarch64_cortex-a53"] != 4_000_000 {
		t.Errorf("manifest size not decoded: %+v", manifest.SizeBytes)
	}

	tok, err := sw.DecodeToken("cashuBfake")
	if err != nil {
		t.Fatalf("DecodeToken: %v", err)
	}
	if tok.Amount() != 21 || tok.Mint() != "https://mint.example" {
		t.Errorf("DecodeToken token = %d/%s", tok.Amount(), tok.Mint())
	}
	if ser, _ := tok.Serialize(); ser != "cashuBfake" {
		t.Errorf("Serialize = %q", ser)
	}
	tok.Close()

	if got, err := sw.Receive(tok); err != nil || got != 42 {
		t.Errorf("Receive = %d, %v", got, err)
	}
	if got := sw.GetBalance(); got != 1000 {
		t.Errorf("GetBalance = %d", got)
	}
	if got := sw.GetBalanceByMint("https://mint.example"); got != 500 {
		t.Errorf("GetBalanceByMint = %d", got)
	}
	if got := sw.GetAllMintBalances()["https://mint.example"]; got != 500 {
		t.Errorf("GetAllMintBalances = %v", got)
	}
	if s, err := sw.SendWithOverpayment(5, "https://mint.example", 1, 1); err != nil || s != "cashuAovp" {
		t.Errorf("SendWithOverpayment = %q, %v", s, err)
	}
	sent, err := sw.Send(5, "https://mint.example", false)
	if err != nil || sent.Amount() != 5 {
		t.Errorf("Send = %v, %v", sent, err)
	}
	if drained, amt, err := sw.Drain("https://mint.example"); err != nil || amt != 500 || drained.Amount() != 500 {
		t.Errorf("Drain = %v %d %v", drained, amt, err)
	}
	if err := sw.MeltToLightning("https://mint.example", 10, 1, "user@example.com"); err != nil {
		t.Errorf("MeltToLightning: %v", err)
	}
	if q, err := sw.RequestMintQuote(21, "https://mint.example"); err != nil || q.QuoteID != "q1" || q.State != StateUnpaid {
		t.Errorf("RequestMintQuote = %+v, %v", q, err)
	}
	if st, err := sw.GetMintQuoteState("q1"); err != nil || st != StatePaid {
		t.Errorf("GetMintQuoteState = %v, %v", st, err)
	}
	if n, err := sw.MintTokens("q1"); err != nil || n != 21 {
		t.Errorf("MintTokens = %d, %v", n, err)
	}
	if q, err := sw.RequestMeltQuote("lnbc1", "https://mint.example"); err != nil || q.QuoteID != "m1" {
		t.Errorf("RequestMeltQuote = %+v, %v", q, err)
	}
	if r, err := sw.Melt("m1"); err != nil || !r.Paid || r.Preimage != "ff" {
		t.Errorf("Melt = %+v, %v", r, err)
	}
	if err := sw.Shutdown(); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func TestSidecarWalletError(t *testing.T) {
	sock := fakeSidecar(t, func(method string, _ json.RawMessage) (any, error) {
		return nil, errors.New("boom: " + method)
	})
	sw, _, err := NewSidecarWallet(sock)
	if err == nil {
		t.Fatal("expected handshake error")
	}
	_ = sw
}

func TestSidecarWalletUnreachable(t *testing.T) {
	_, _, err := NewSidecarWallet(filepath.Join(t.TempDir(), "nope.sock"))
	if !errors.Is(err, ErrSidecarNotConnected) {
		t.Fatalf("expected ErrSidecarNotConnected, got %v", err)
	}
}

var _ WalletPort = (*SidecarWallet)(nil)
