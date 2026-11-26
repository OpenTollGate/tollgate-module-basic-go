//go:build cdk_wallet

package tollwallet

// The CDK sidecar daemon: the process side of the seam this repo already
// declares. `select.go` routes a "cdk"/"sidecar" backend to `NewSidecarWallet`,
// which speaks newline-delimited JSON over AF_UNIX — and until now nothing on
// the other end of that socket existed, so the CDK backend was unreachable.
//
// Why a separate process rather than linking cdk-go into the router: cdk-go
// reaches libcdk through cgo FFI (`cashubtc/cdk-go/bindings/cdkffi`), and
// `CGO_ENABLED=0 GOOS=linux GOARCH=mipsle` (and arm64) fail at that package. The
// router binary must stay pure Go for every OpenWrt target, so the wallet runs
// here and the module talks to it. This file is therefore built only with
// `-tags cdk_wallet`, and it is the one place allowed to be cgo.
//
// This is a thin adapter deliberately. Every method below forwards to the
// already-tested CdkWallet; no money logic lives here. Two things it does own,
// because they are properties of being a server rather than of being a wallet:
//
//   - One request cannot kill the process. A malformed line is answered with an
//     error and the connection ends; a panic in one handler would take the
//     wallet down for every caller, so handlers run behind a recover.
//   - The socket is not world-writable. It controls money.
//
// Concurrency: the wallet is not safe for concurrent callers (CDK's sqlite
// backend is single-writer), so requests are serialised through one mutex. The
// protocol carries an id per request and echoes it, so a client can still
// pipeline; it simply will not get parallel execution of wallet state changes.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
)

// SidecarDaemon serves WalletPort over the sidecar protocol.
type SidecarDaemon struct {
	wallet WalletPort
	mu     sync.Mutex // serialises wallet calls; the wallet is single-writer
	wg     sync.WaitGroup
}

// NewSidecarDaemon wraps a wallet. It does not take ownership of closing it:
// the caller opened it and the caller closes it.
func NewSidecarDaemon(wallet WalletPort) *SidecarDaemon {
	return &SidecarDaemon{wallet: wallet}
}

// sidecarRequest and sidecarResponse are the protocol's own shapes; they are
// declared in sidecar.go and reused here so the two ends cannot drift.

// Serve listens on sock and answers until ctx is cancelled. It closes ready when
// the socket is bound, so a caller can connect without polling, and removes the
// socket file on exit so a restart does not inherit a stale one.
func (d *SidecarDaemon) Serve(ctx context.Context, sock string, ready chan<- struct{}) error {
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}
	// A stale socket from a crash is not a live daemon: probe before removing, so
	// this never steals a socket from a daemon that is actually running.
	if _, err := os.Stat(sock); err == nil {
		if c, derr := net.Dial("unix", sock); derr == nil {
			c.Close()
			return fmt.Errorf("another daemon is already serving %s", sock)
		}
		if err := os.Remove(sock); err != nil {
			return fmt.Errorf("remove stale socket: %w", err)
		}
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return fmt.Errorf("listen %s: %w", sock, err)
	}
	// The socket controls money; nobody outside this user may talk to it.
	if err := os.Chmod(sock, 0o600); err != nil {
		ln.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}
	defer func() {
		ln.Close()
		os.Remove(sock)
	}()

	if ready != nil {
		close(ready)
	}

	// Cancellation closes the listener, which unblocks Accept.
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				d.wg.Wait()
				return nil
			default:
			}
			// A transient accept error must not stop the wallet.
			continue
		}
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			d.serveConn(conn)
		}()
	}
}

// Wait blocks until every in-flight connection has finished.
func (d *SidecarDaemon) Wait() { d.wg.Wait() }

func (d *SidecarDaemon) serveConn(conn net.Conn) {
	defer conn.Close()
	dec := json.NewDecoder(bufio.NewReader(conn))
	enc := json.NewEncoder(conn)

	for {
		var req sidecarRequest
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			// Answer, then end the connection: the stream is no longer
			// trustworthy after a framing error.
			_ = enc.Encode(sidecarResponse{ID: req.ID, OK: false,
				Error: "malformed request: " + err.Error()})
			return
		}
		resp := d.handle(req)
		if err := enc.Encode(resp); err != nil {
			return
		}
	}
}

// handle runs one request. It recovers from a panic so one bad call cannot take
// the wallet down for every other client.
func (d *SidecarDaemon) handle(req sidecarRequest) (resp sidecarResponse) {
	defer func() {
		if r := recover(); r != nil {
			resp = sidecarResponse{ID: req.ID, OK: false,
				Error: fmt.Sprintf("internal error: %v", r)}
		}
	}()

	d.mu.Lock()
	defer d.mu.Unlock()

	result, err := d.dispatch(req.Method, req.Params)
	if err != nil {
		return sidecarResponse{ID: req.ID, OK: false, Error: err.Error()}
	}
	blob, err := json.Marshal(result)
	if err != nil {
		return sidecarResponse{ID: req.ID, OK: false,
			Error: "encode result: " + err.Error()}
	}
	return sidecarResponse{ID: req.ID, OK: true, Result: blob}
}

// dispatch is the method table. The names are the client's, declared once in
// sidecar.go's caller and mirrored here; a mismatch would surface as "unknown
// method" at runtime, which is why the tests drive the real client.
func (d *SidecarDaemon) dispatch(method string, raw json.RawMessage) (any, error) {
	switch method {
	case "info":
		// Capability manifest. The client treats a nil result as "no manifest".
		return map[string]any{"backend": "cdk", "unit": "sat"}, nil

	case "get_balance":
		return d.wallet.GetBalance(), nil

	case "get_balance_by_mint":
		var p struct {
			MintURL string `json:"mint_url"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		return d.wallet.GetBalanceByMint(p.MintURL), nil

	case "get_all_mint_balances":
		return d.wallet.GetAllMintBalances(), nil

	case "decode_token":
		var p struct {
			Token string `json:"token"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		tok, err := d.wallet.DecodeToken(p.Token)
		if err != nil {
			return nil, err
		}
		defer tok.Close()
		// The wire form is the token re-encoded, which is what the client's
		// own decode_token consumer expects to hand on.
		return p.Token, nil

	case "swap_fee_sats":
		var p struct {
			Token string `json:"token"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		tok, err := d.wallet.DecodeToken(p.Token)
		if err != nil {
			return nil, err
		}
		defer tok.Close()
		return d.wallet.SwapFeeSats(tok)

	case "receive":
		var p struct {
			Token string `json:"token"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		tok, err := d.wallet.DecodeToken(p.Token)
		if err != nil {
			return nil, err
		}
		defer tok.Close()
		return d.wallet.Receive(tok)

	case "accept_mint":
		var p struct {
			MintURL string `json:"mint_url"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		return nil, d.wallet.AcceptMint(p.MintURL)

	case "send":
		var p struct {
			Amount      uint64 `json:"amount"`
			MintURL     string `json:"mint_url"`
			IncludeFees bool   `json:"include_fees"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		tok, err := d.wallet.Send(p.Amount, p.MintURL, p.IncludeFees)
		if err != nil {
			return nil, err
		}
		defer tok.Close()
		return tok.Serialize()

	case "send_with_overpayment":
		var p struct {
			Amount                 uint64 `json:"amount"`
			MintURL                string `json:"mint_url"`
			MaxOverpaymentPercent  uint64 `json:"max_overpayment_percent"`
			MaxOverpaymentAbsolute uint64 `json:"max_overpayment_absolute"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		return d.wallet.SendWithOverpayment(p.Amount, p.MintURL,
			p.MaxOverpaymentPercent, p.MaxOverpaymentAbsolute)

	case "drain":
		var p struct {
			MintURL string `json:"mint_url"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		tok, amount, err := d.wallet.Drain(p.MintURL)
		if err != nil {
			return nil, err
		}
		if tok != nil {
			defer tok.Close()
			ser, serr := tok.Serialize()
			if serr != nil {
				return nil, serr
			}
			return map[string]any{"token": ser, "amount": amount}, nil
		}
		return map[string]any{"token": "", "amount": amount}, nil

	case "melt_to_lightning":
		var p struct {
			MintURL      string `json:"mint_url"`
			TargetAmount uint64 `json:"target_amount"`
			MaxCost      uint64 `json:"max_cost"`
			LNURL        string `json:"lnurl"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		return nil, d.wallet.MeltToLightning(p.MintURL, p.TargetAmount, p.MaxCost, p.LNURL)

	case "request_mint_quote":
		var p struct {
			Amount  uint64 `json:"amount"`
			MintURL string `json:"mint_url"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		return d.wallet.RequestMintQuote(p.Amount, p.MintURL)

	case "mint_quote_state":
		var p struct {
			QuoteID string `json:"quote_id"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		return d.wallet.GetMintQuoteState(p.QuoteID)

	case "mint_tokens":
		var p struct {
			QuoteID string `json:"quote_id"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		return d.wallet.MintTokens(p.QuoteID)

	case "request_melt_quote":
		var p struct {
			Invoice string `json:"invoice"`
			MintURL string `json:"mint_url"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		return d.wallet.RequestMeltQuote(p.Invoice, p.MintURL)

	case "melt":
		var p struct {
			QuoteID string `json:"quote_id"`
		}
		if err := decodeParams(raw, &p); err != nil {
			return nil, err
		}
		return d.wallet.Melt(p.QuoteID)

	case "shutdown":
		// Acknowledged, never acted on from here: the operator stops the daemon,
		// not a client. Acting on it would let any local caller take the wallet
		// down mid-transaction.
		return map[string]any{"shutdown": "ignored"}, nil
	}

	if sidecarReadOnlyMethods[method] {
		// Declared read-only by the client but not implemented here: say so
		// rather than pretending.
		return nil, fmt.Errorf("method %q is not implemented by this daemon", method)
	}
	return nil, fmt.Errorf("unknown method %q", method)
}

func decodeParams(raw json.RawMessage, into any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("bad params: %w", err)
	}
	return nil
}
