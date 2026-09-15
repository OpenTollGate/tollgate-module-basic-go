package tollwallet

// Sidecar wallet backend: a WalletPort implementation that talks to a separate
// wallet daemon over an AF_UNIX socket using newline-delimited JSON. This lets
// the router select a wallet backend (CDK, nucula, …) at runtime without CGO
// and without linking any Cashu library into the service.
//
// Protocol (one request per line, one response per line):
//
//	-> {"id":1,"method":"get_balance","params":{}}
//	<- {"id":1,"ok":true,"result":1234}
//	<- {"id":1,"ok":false,"error":"..."}
//
// The method names mirror WalletPort. `info` returns the daemon's capability
// manifest (see manifest.go). See the wallet-selection architecture on the
// research branch.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// ErrSidecarNotConnected is returned when the daemon cannot be reached.
var ErrSidecarNotConnected = errors.New("tollwallet: wallet sidecar not connected")

type sidecarRequest struct {
	ID     uint64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type sidecarResponse struct {
	ID     uint64          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// SidecarWallet implements WalletPort over the sidecar RPC.
type SidecarWallet struct {
	socketPath string
	timeout    time.Duration

	mu     sync.Mutex
	conn   net.Conn
	rd     *bufio.Reader
	nextID uint64
}

// NewSidecarWallet connects to the wallet daemon at socketPath and performs the
// `info` handshake, returning both the port and the daemon's capability
// manifest.
func NewSidecarWallet(socketPath string) (*SidecarWallet, *Manifest, error) {
	sw := &SidecarWallet{socketPath: socketPath, timeout: 30 * time.Second}
	var m Manifest
	if err := sw.call("info", nil, &m); err != nil {
		return nil, nil, err
	}
	return sw, &m, nil
}

// Info returns the daemon's capability manifest.
func (s *SidecarWallet) Info() (*Manifest, error) {
	var m Manifest
	if err := s.call("info", nil, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *SidecarWallet) connectLocked() error {
	conn, err := net.DialTimeout("unix", s.socketPath, s.timeout)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSidecarNotConnected, err)
	}
	s.conn = conn
	s.rd = bufio.NewReader(conn)
	return nil
}

// call performs one RPC round-trip. It reconnects once on a lost connection.
func (s *SidecarWallet) call(method string, params any, out any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		raw = b
	}

	for attempt := 0; attempt < 2; attempt++ {
		if s.conn == nil {
			if err := s.connectLocked(); err != nil {
				return err
			}
		}
		s.nextID++
		req := sidecarRequest{ID: s.nextID, Method: method, Params: raw}
		line, err := json.Marshal(req)
		if err != nil {
			return err
		}
		if err := s.conn.SetDeadline(time.Now().Add(s.timeout)); err == nil {
			if _, err = s.conn.Write(append(line, '\n')); err == nil {
				var respLine []byte
				if respLine, err = s.rd.ReadBytes('\n'); err == nil {
					var resp sidecarResponse
					if err = json.Unmarshal(respLine, &resp); err == nil {
						if !resp.OK {
							return fmt.Errorf("tollwallet: sidecar %s: %s", method, resp.Error)
						}
						if out != nil && len(resp.Result) > 0 {
							return json.Unmarshal(resp.Result, out)
						}
						return nil
					}
				}
			}
		}
		// connection failed mid-call: drop it and retry once
		s.conn.Close()
		s.conn = nil
		s.rd = nil
	}
	return fmt.Errorf("%w: %s", ErrSidecarNotConnected, method)
}

func (s *SidecarWallet) shutdownConn() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
		s.rd = nil
	}
}

// sidecarToken is the Token returned by the sidecar backend.
type sidecarToken struct {
	mint       string
	amount     uint64
	serialized string
}

// Mint returns the mint URL embedded in the token.
func (t *sidecarToken) Mint() string { return t.mint }

// Amount returns the token value in its unit.
func (t *sidecarToken) Amount() uint64 { return t.amount }

// Serialize returns the canonical Cashu token string.
func (t *sidecarToken) Serialize() (string, error) {
	if t.serialized == "" {
		return "", errors.New("tollwallet: sidecar token has no serialization")
	}
	return t.serialized, nil
}

// Close is a no-op: the sidecar has no CGO resources in this process.
func (t *sidecarToken) Close() {}

type tokenJSON struct {
	Token  string `json:"token"`
	Mint   string `json:"mint"`
	Amount uint64 `json:"amount"`
}

// DecodeToken parses a Cashu token via the daemon.
func (s *SidecarWallet) DecodeToken(tokenStr string) (Token, error) {
	var r tokenJSON
	if err := s.call("decode_token", map[string]string{"token": tokenStr}, &r); err != nil {
		return nil, err
	}
	if r.Token == "" {
		r.Token = tokenStr
	}
	return &sidecarToken{mint: r.Mint, amount: r.Amount, serialized: r.Token}, nil
}

// Receive accepts a token and credits the wallet.
func (s *SidecarWallet) Receive(token Token) (uint64, error) {
	ser, err := token.Serialize()
	if err != nil {
		return 0, err
	}
	var amount uint64
	if err := s.call("receive", map[string]string{"token": ser}, &amount); err != nil {
		return 0, err
	}
	return amount, nil
}

// GetBalance returns the total balance across mints.
func (s *SidecarWallet) GetBalance() uint64 {
	var v uint64
	_ = s.call("get_balance", nil, &v)
	return v
}

// GetBalanceByMint returns the balance for one mint.
func (s *SidecarWallet) GetBalanceByMint(mintUrl string) uint64 {
	var v uint64
	_ = s.call("get_balance_by_mint", map[string]string{"mint_url": mintUrl}, &v)
	return v
}

// GetAllMintBalances returns per-mint balances.
func (s *SidecarWallet) GetAllMintBalances() map[string]uint64 {
	out := map[string]uint64{}
	_ = s.call("get_all_mint_balances", nil, &out)
	return out
}

// SendWithOverpayment sends with overpayment tolerance, returning a token string.
func (s *SidecarWallet) SendWithOverpayment(amount uint64, mintUrl string, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error) {
	params := map[string]any{
		"amount": amount, "mint_url": mintUrl,
		"max_overpayment_percent":  maxOverpaymentPercent,
		"max_overpayment_absolute": maxOverpaymentAbsolute,
	}
	var out string
	if err := s.call("send_with_overpayment", params, &out); err != nil {
		return "", err
	}
	return out, nil
}

// Send creates a token of the given amount.
func (s *SidecarWallet) Send(amount uint64, mintUrl string, includeFees bool) (Token, error) {
	params := map[string]any{"amount": amount, "mint_url": mintUrl, "include_fees": includeFees}
	var r tokenJSON
	if err := s.call("send", params, &r); err != nil {
		return nil, err
	}
	return &sidecarToken{mint: r.Mint, amount: r.Amount, serialized: r.Token}, nil
}

// Drain creates a token for the full balance of a mint.
func (s *SidecarWallet) Drain(mintUrl string) (Token, uint64, error) {
	var r struct {
		Token  string `json:"token"`
		Mint   string `json:"mint"`
		Amount uint64 `json:"amount"`
	}
	if err := s.call("drain", map[string]string{"mint_url": mintUrl}, &r); err != nil {
		return nil, 0, err
	}
	return &sidecarToken{mint: r.Mint, amount: r.Amount, serialized: r.Token}, r.Amount, nil
}

// MeltToLightning pays a Lightning address from wallet funds.
func (s *SidecarWallet) MeltToLightning(mintUrl string, targetAmount uint64, maxCost uint64, lnurl string) error {
	params := map[string]any{"mint_url": mintUrl, "target_amount": targetAmount, "max_cost": maxCost, "lnurl": lnurl}
	return s.call("melt_to_lightning", params, nil)
}

// RequestMintQuote asks the mint for a bolt11 mint quote.
func (s *SidecarWallet) RequestMintQuote(amount uint64, mintUrl string) (*MintQuote, error) {
	var q MintQuote
	if err := s.call("request_mint_quote", map[string]any{"amount": amount, "mint_url": mintUrl}, &q); err != nil {
		return nil, err
	}
	return &q, nil
}

// GetMintQuoteState returns the state of a mint quote.
func (s *SidecarWallet) GetMintQuoteState(quoteID string) (MintQuoteState, error) {
	var st MintQuoteState
	if err := s.call("mint_quote_state", map[string]string{"quote_id": quoteID}, &st); err != nil {
		return StateUnknown, err
	}
	return st, nil
}

// MintTokens mints tokens for a paid quote.
func (s *SidecarWallet) MintTokens(quoteID string) (uint64, error) {
	var v uint64
	if err := s.call("mint_tokens", map[string]string{"quote_id": quoteID}, &v); err != nil {
		return 0, err
	}
	return v, nil
}

// RequestMeltQuote asks for a melt quote for a bolt11 invoice.
func (s *SidecarWallet) RequestMeltQuote(invoice string, mintUrl string) (*MeltQuote, error) {
	var q MeltQuote
	if err := s.call("request_melt_quote", map[string]string{"invoice": invoice, "mint_url": mintUrl}, &q); err != nil {
		return nil, err
	}
	return &q, nil
}

// Melt executes a melt quote.
func (s *SidecarWallet) Melt(quoteID string) (*MeltResult, error) {
	var r MeltResult
	if err := s.call("melt", map[string]string{"quote_id": quoteID}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Shutdown closes the sidecar connection. It is idempotent.
func (s *SidecarWallet) Shutdown() error {
	_ = s.call("shutdown", nil, nil)
	s.shutdownConn()
	return nil
}
