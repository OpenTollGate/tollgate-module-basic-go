// cdkinterop: drives the CDK wallet sidecar daemon over the WalletPort sidecar
// RPC. Subcommands let the physical-router test suite exercise both the happy
// path and error/edge cases from the device.
//
// Usage:
//
//	cdkinterop <socket> info
//	cdkinterop <socket> balance
//	cdkinterop <socket> mintquote <amount>
//	cdkinterop <socket> mqstate <quote_id>
//	cdkinterop <socket> decode <token>
//	cdkinterop <socket> receive <token>
//	cdkinterop <socket> send <amount>
//
// Prints one JSON object: {"cmd":...,"ok":bool,"<field>":...} or {"error":...}.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

func emit(m map[string]any) {
	b, _ := json.Marshal(m)
	fmt.Println(string(b))
}

func fail(cmd string, err error) {
	emit(map[string]any{"cmd": cmd, "ok": false, "error": err.Error()})
	os.Exit(0)
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: cdkinterop <socket> <info|balance|mintquote|mqstate|decode|receive|send> [arg]")
		os.Exit(2)
	}
	sock, cmd := os.Args[1], os.Args[2]
	arg := ""
	if len(os.Args) > 3 {
		arg = os.Args[3]
	}

	w, m, err := tollwallet.NewSidecarWallet(sock)
	if err != nil {
		// Report a structured failure so tests can assert on it (missing socket).
		emit(map[string]any{"cmd": cmd, "ok": false, "error": err.Error()})
		os.Exit(0)
	}

	switch cmd {
	case "info":
		emit(map[string]any{
			"cmd": cmd, "ok": true, "backend": m.Backend, "kind": m.Kind,
			"licence": m.Licence, "arches": m.Arches,
		})
	case "balance":
		emit(map[string]any{"cmd": cmd, "ok": true, "balance": w.GetBalance()})
	case "mintquote":
		amt := uint64(10)
		if arg != "" {
			fmt.Sscanf(arg, "%d", &amt)
		}
		q, err := w.RequestMintQuote(amt, "")
		if err != nil {
			fail(cmd, err)
		}
		emit(map[string]any{"cmd": cmd, "ok": true, "quote_id": q.QuoteID, "has_request": q.Request != ""})
	case "mqstate":
		st, err := w.GetMintQuoteState(arg)
		if err != nil {
			fail(cmd, err)
		}
		emit(map[string]any{"cmd": cmd, "ok": true, "state": st.String()})
	case "mint":
		n, err := w.MintTokens(arg)
		if err != nil {
			fail(cmd, err)
		}
		emit(map[string]any{"cmd": cmd, "ok": true, "minted": n})
	case "decode":
		t, err := w.DecodeToken(arg)
		if err != nil {
			fail(cmd, err)
		}
		emit(map[string]any{"cmd": cmd, "ok": true, "amount": t.Amount(), "mint": t.Mint()})
		t.Close()
	case "receive":
		// Feed a raw token string through DecodeToken then Receive.
		t, err := w.DecodeToken(arg)
		if err != nil {
			fail(cmd, err)
		}
		n, err := w.Receive(t)
		if err != nil {
			fail(cmd, err)
		}
		emit(map[string]any{"cmd": cmd, "ok": true, "received": n})
	case "send":
		amt := uint64(1)
		if arg != "" {
			fmt.Sscanf(arg, "%d", &amt)
		}
		t, err := w.Send(amt, "", false)
		if err != nil {
			fail(cmd, err)
		}
		s, _ := t.Serialize()
		emit(map[string]any{"cmd": cmd, "ok": true, "token": s, "token_len": len(s)})
	case "send_p2pk":
		amt := uint64(1)
		if arg != "" {
			fmt.Sscanf(arg, "%d", &amt)
		}
		pk := ""
		if len(os.Args) > 4 {
			pk = os.Args[4]
		}
		var res struct {
			Token  string `json:"token"`
			Amount uint64 `json:"amount"`
		}
		if err := w.Call("send_p2pk", map[string]any{"amount": amt, "pubkey": pk}, &res); err != nil {
			fail(cmd, err)
		}
		emit(map[string]any{"cmd": cmd, "ok": true, "token": res.Token, "token_len": len(res.Token)})
	default:
		// Unknown method: the sidecar client should surface the daemon error.
		emit(map[string]any{"cmd": cmd, "ok": false, "error": "unknown subcommand " + cmd})
	}

	_ = w.Shutdown()
}
