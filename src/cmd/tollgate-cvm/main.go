// tollgate-cvm exposes the local TollGate CLI socket over ContextVM (CVM) on
// Nostr: kind-25910 JSON-RPC inside NIP-44/NIP-59 gift wraps, announced via
// CEP-6, authorized by the maintainer npub in config.json.
//
// Outbound wss only — the router opens no listening port.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

func main() {
	cfg, err := LoadServerConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tollgate-cvm: %v\n", err)
		os.Exit(1)
	}

	bridge := NewSocketBridge(cfg.SocketPath, 30*time.Second)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverPK, err := nostr.GetPublicKey(cfg.ServerKeyHex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tollgate-cvm: server key invalid: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("tollgate-cvm: serving %s on %v\n", serverPK, cfg.Relays)

	if err := Serve(ctx, cfg, bridge); err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "tollgate-cvm: %v\n", err)
		os.Exit(1)
	}
}
