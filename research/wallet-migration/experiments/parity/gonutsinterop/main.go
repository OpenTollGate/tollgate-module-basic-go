// gonutsinterop drives the in-process gonuts wallet through the same
// library-agnostic WalletPort the sidecar client uses, so T2e can compare
// gonuts against CDK on identical fixed inputs.
//
// Usage:
//
//	gonutsinterop <walletdir> <mint> balance
//	gonutsinterop <walletdir> <mint> mintquote <amount>
//	gonutsinterop <walletdir> <mint> mqstate <quote_id>
//	gonutsinterop <walletdir> <mint> mint <quote_id>
//	gonutsinterop <walletdir> <mint> decode <token>
//	gonutsinterop <walletdir> <mint> receive <token>
//	gonutsinterop <walletdir> <mint> send <amount>
//	gonutsinterop <walletdir> <mint> drain
//	gonutsinterop <walletdir> <mint> meltquote <invoice>
//	gonutsinterop <walletdir> <mint> melt <quote_id>
//
// Prints one JSON object per run, matching cdkinterop's shape.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	gonwallet "github.com/OpenTollGate/gonuts-tollgate/wallet"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

func emit(m map[string]any) {
	b, _ := json.Marshal(m)
	fmt.Println(string(b))
}

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: gonutsinterop <walletdir> <mint> <cmd> [arg]")
		os.Exit(2)
	}
	dir, mint, cmd := os.Args[1], os.Args[2], os.Args[3]
	arg := ""
	if len(os.Args) > 4 {
		arg = os.Args[4]
	}

	w, err := tollwallet.NewWalletPort(dir, []string{mint}, false)
	if err != nil {
		emit(map[string]any{"cmd": cmd, "ok": false, "error": err.Error()})
		return
	}
	defer func() { _ = w.Shutdown() }()

	switch cmd {
	case "seed":
		// Migration aid: expose gonuts' stored BIP-39 mnemonic (from wallet.db)
		// so a same-seed NUT-09 restore into CDK can be attempted.
		gw, err := gonwallet.LoadWallet(gonwallet.Config{WalletPath: dir, CurrentMintURL: mint})
		if err != nil {
			emit(map[string]any{"cmd": cmd, "ok": false, "error": err.Error()})
			return
		}
		emit(map[string]any{"cmd": cmd, "ok": true, "mnemonic": gw.Mnemonic()})

	case "balance":
		emit(map[string]any{"cmd": cmd, "ok": true, "balance": w.GetBalance()})

	case "mintquote":
		var amt uint64 = 10
		fmt.Sscanf(arg, "%d", &amt)
		q, err := w.RequestMintQuote(amt, mint)
		if err != nil {
			emit(map[string]any{"cmd": cmd, "ok": false, "error": err.Error()})
			return
		}
		emit(map[string]any{"cmd": cmd, "ok": true, "quote_id": q.QuoteID,
			"has_request": q.Request != "", "state": q.State.String(), "amount": q.Amount})

	case "mqstate":
		st, err := w.GetMintQuoteState(arg)
		if err != nil {
			emit(map[string]any{"cmd": cmd, "ok": false, "error": err.Error()})
			return
		}
		emit(map[string]any{"cmd": cmd, "ok": true, "state": st.String()})

	case "mint":
		n, err := w.MintTokens(arg)
		if err != nil {
			emit(map[string]any{"cmd": cmd, "ok": false, "error": err.Error()})
			return
		}
		emit(map[string]any{"cmd": cmd, "ok": true, "minted": n})

	case "decode":
		t, err := w.DecodeToken(arg)
		if err != nil {
			emit(map[string]any{"cmd": cmd, "ok": false, "error": err.Error()})
			return
		}
		emit(map[string]any{"cmd": cmd, "ok": true, "amount": t.Amount(), "mint": t.Mint()})
		t.Close()

	case "receive":
		t, err := w.DecodeToken(arg)
		if err != nil {
			emit(map[string]any{"cmd": cmd, "ok": false, "error": err.Error()})
			return
		}
		n, err := w.Receive(t)
		t.Close()
		if err != nil {
			emit(map[string]any{"cmd": cmd, "ok": false, "error": err.Error()})
			return
		}
		emit(map[string]any{"cmd": cmd, "ok": true, "received": n})

	case "send":
		var amt uint64 = 1
		fmt.Sscanf(arg, "%d", &amt)
		t, err := w.Send(amt, mint, false)
		if err != nil {
			emit(map[string]any{"cmd": cmd, "ok": false, "error": err.Error()})
			return
		}
		s, _ := t.Serialize()
		t.Close()
		emit(map[string]any{"cmd": cmd, "ok": true, "token": s, "token_len": len(s)})

	case "drain":
		t, amt, err := w.Drain(mint)
		if err != nil {
			emit(map[string]any{"cmd": cmd, "ok": false, "error": err.Error()})
			return
		}
		s, _ := t.Serialize()
		t.Close()
		emit(map[string]any{"cmd": cmd, "ok": true, "amount": amt, "token": s, "token_len": len(s)})

	case "meltquote":
		q, err := w.RequestMeltQuote(arg, mint)
		if err != nil {
			emit(map[string]any{"cmd": cmd, "ok": false, "error": err.Error()})
			return
		}
		emit(map[string]any{"cmd": cmd, "ok": true, "quote_id": q.QuoteID,
			"amount": q.Amount, "fee_reserve": q.FeeReserve, "state": q.State.String()})

	case "melt":
		r, err := w.Melt(arg)
		if err != nil {
			emit(map[string]any{"cmd": cmd, "ok": false, "error": err.Error()})
			return
		}
		emit(map[string]any{"cmd": cmd, "ok": true, "paid": r.Paid, "preimage": r.Preimage})

	default:
		emit(map[string]any{"cmd": cmd, "ok": false, "error": "unknown subcommand " + cmd})
	}
}
