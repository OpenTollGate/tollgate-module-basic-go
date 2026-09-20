package tollwallet

// Wallet selection: turn a policy + config into a concrete WalletPort. This is
// the consumer that lets the router "choose the wallet best suited to the
// target" — an in-process backend (gonuts, default) or a separate daemon
// (sidecar: CDK, nucula) — without the rest of the module knowing which.

import (
	"fmt"
)

// WalletConfig is the runtime configuration for opening a wallet backend.
type WalletConfig struct {
	// Backend selects the implementation: "" or "gonuts" (in-process default),
	// or "cdk"/"nucula"/"sidecar" (start/connect to a daemon).
	Backend string
	// Path is the in-process wallet directory.
	Path string
	// Socket is the AF_UNIX socket of the sidecar daemon.
	Socket string
	// AcceptedMints / AllowAndSwapUntrustedMints are the in-process options.
	AcceptedMints              []string
	AllowAndSwapUntrustedMints bool
}

// OpenWallet opens the configured backend. For sidecar backends it returns the
// daemon's capability manifest; for in-process backends the manifest is nil.
func OpenWallet(cfg WalletConfig) (WalletPort, *Manifest, error) {
	switch cfg.Backend {
	case "", "gonuts", "default", "in_process":
		p, err := NewWalletPort(cfg.Path, cfg.AcceptedMints, cfg.AllowAndSwapUntrustedMints)
		return p, nil, err
	case "cdk", "nucula", "sidecar":
		if cfg.Socket == "" {
			return nil, nil, fmt.Errorf("tollwallet: backend %q needs a socket", cfg.Backend)
		}
		return NewSidecarWallet(cfg.Socket)
	default:
		return nil, nil, fmt.Errorf("tollwallet: unknown wallet backend %q", cfg.Backend)
	}
}

// OpenFor selects the backend for a target (arch + flash tier) from the policy
// and opens it. An explicit cfg.Backend overrides the policy (operator choice).
func (p *WalletPolicy) OpenFor(arch string, flashMB int, cfg WalletConfig) (WalletPort, *Manifest, error) {
	if cfg.Backend == "" && p != nil {
		if backend, socket, ok := p.SelectBackend(arch, flashMB); ok {
			cfg.Backend = backend
			if cfg.Socket == "" {
				cfg.Socket = socket
			}
		}
	}
	return OpenWallet(cfg)
}
