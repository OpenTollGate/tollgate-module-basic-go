package hedgehog

// BitcoinCoreChainView talks to a bitcoind over JSON-RPC.
//
// This is the CHAIN VIEW USED BY THE TESTS AND THE BENCH, not the one the router
// uses. The router has no bitcoind (the PR #944 research confirmed no
// bitcoind/bitcoin-cli on the device and no chain state), so it uses an
// Electrum/SPV endpoint or the NullChainView. This implementation exists so the
// protocol can be validated against REAL consensus on the bench, which is the
// whole point of having a pluggable interface.
//
// It deliberately implements only the three ChainView methods. A full RPC client
// would drag in far more surface than the protocol needs.

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// CoreConfig configures a BitcoinCoreChainView.
type CoreConfig struct {
	// URL is the JSON-RPC endpoint, e.g. http://127.0.0.1:18443.
	URL string
	// User and Pass are the RPC credentials. They are sent via HTTP basic auth,
	// so the URL should be loopback or otherwise trusted; never log Pass.
	User, Pass string
	// Wallet, when set, is added as /wallet/<name> to reach wallet-scoped RPCs.
	Wallet string
	// Timeout bounds each request. Defaults to 20s.
	Timeout time.Duration
}

// BitcoinCoreChainView implements ChainView against bitcoind.
type BitcoinCoreChainView struct {
	cfg  CoreConfig
	http *http.Client
}

// NewBitcoinCoreChainView validates the config and returns a view.
func NewBitcoinCoreChainView(cfg CoreConfig) (*BitcoinCoreChainView, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("hedgehog: core URL is required")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 20 * time.Second
	}
	return &BitcoinCoreChainView{
		cfg:  cfg,
		http: &http.Client{Timeout: cfg.Timeout},
	}, nil
}

type coreRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type coreResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// call performs one JSON-RPC call.
//
// Wallet scope is chosen per-call because `listtransactions`-style calls need it
// while `getblockchaininfo` does not, and mixing them on one client produces the
// confusing "Multiple wallets are loaded" error.
func (c *BitcoinCoreChainView) call(ctx context.Context, walletScoped bool, method string, params ...any) (json.RawMessage, error) {
	url := c.cfg.URL
	if walletScoped && c.cfg.Wallet != "" {
		url = fmt.Sprintf("%s/wallet/%s", c.cfg.URL, c.cfg.Wallet)
	}
	body, err := json.Marshal(coreRequest{
		JSONRPC: "1.0", ID: 1, Method: method, Params: params,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.User != "" {
		req.SetBasicAuth(c.cfg.User, c.cfg.Pass)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hedgehog: core %s: %w", method, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var cr coreResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return nil, fmt.Errorf("hedgehog: core %s: bad response: %w", method, err)
	}
	if cr.Error != nil {
		// -5 is "No such mempool or blockchain transaction"; that is absence,
		// not failure, and the ChainView contract says absence is not an error.
		if cr.Error.Code == -5 {
			return nil, ErrTxNotFound
		}
		return nil, fmt.Errorf("hedgehog: core %s: %s (code %d)",
			method, cr.Error.Message, cr.Error.Code)
	}
	return cr.Result, nil
}

// GetTxStatus implements ChainView using getrawtransaction + gettxout.
func (c *BitcoinCoreChainView) GetTxStatus(ctx context.Context, txid string) (TxStatus, error) {
	// verbose=true gives confirmations and blockhash without needing the index
	// for anything else.
	raw, err := c.call(ctx, false, "getrawtransaction", txid, true)
	if err != nil {
		if err == ErrTxNotFound {
			return TxStatus{}, nil
		}
		return TxStatus{}, err
	}
	var tx struct {
		Confirmations int64  `json:"confirmations"`
		BlockHash     string `json:"blockhash"`
		InMempool     bool   `json:"in_mempool"`
	}
	if err := json.Unmarshal(raw, &tx); err != nil {
		return TxStatus{}, err
	}
	st := TxStatus{}
	if tx.Confirmations > 0 {
		st.Confirmed = true
		// Convert depth to a height using the tip.
		if tip, err := c.TipHeight(ctx); err == nil {
			st.Height = tip - int32(tx.Confirmations) + 1
		}
	} else {
		// No confirmations and the tx is known: treat as mempool. getmempoolentry
		// would confirm it, but a known tx with 0 confirmations is good enough
		// for the protocol's decisions.
		st.Mempool = true
	}
	return st, nil
}

// GetTxOutput implements ChainView via getrawtransaction verbose.
func (c *BitcoinCoreChainView) GetTxOutput(ctx context.Context, txid string, vout uint32) (TxOut, error) {
	raw, err := c.call(ctx, false, "getrawtransaction", txid, true)
	if err != nil {
		return TxOut{}, err
	}
	var tx struct {
		Vout []struct {
			Value        float64 `json:"value"`
			N            uint32  `json:"n"`
			ScriptPubKey struct {
				Hex string `json:"hex"`
			} `json:"scriptPubKey"`
		} `json:"vout"`
	}
	if err := json.Unmarshal(raw, &tx); err != nil {
		return TxOut{}, err
	}
	for _, o := range tx.Vout {
		if o.N == vout {
			spk, err := hex.DecodeString(o.ScriptPubKey.Hex)
			if err != nil {
				return TxOut{}, err
			}
			return TxOut{
				Value:        int64(o.Value*1e8 + 0.5),
				ScriptPubKey: spk,
			}, nil
		}
	}
	return TxOut{}, ErrVoutNotFound
}

// TipHeight implements ChainView.
func (c *BitcoinCoreChainView) TipHeight(ctx context.Context) (int32, error) {
	raw, err := c.call(ctx, false, "getblockcount")
	if err != nil {
		return 0, err
	}
	var n int32
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, err
	}
	return n, nil
}

// CallRaw exposes a wallet-scoped RPC so the regtest lifecycle test can fund
// outputs and mine blocks without a second client. It is not part of ChainView.
func (c *BitcoinCoreChainView) CallRaw(ctx context.Context, walletScoped bool, method string, params ...any) (json.RawMessage, error) {
	return c.call(ctx, walletScoped, method, params...)
}
