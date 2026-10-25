package main

import (
	"fmt"
	"os"
	"strings"
)

// ServerConfig is everything tollgate-cvm needs at boot, resolved from the
// environment once. Secrets (the server nsec) arrive ONLY via env var — never
// argv (visible in ps) and never a flag.
type ServerConfig struct {
	// ServerKeyHex is this daemon's Nostr private key (64 hex chars). It is
	// the identity the kind-11316 announcement is published under and the key
	// that NIP-44-decrypts incoming requests.
	ServerKeyHex string
	// MaintainerNpub seeds the Authorizer. An empty value means "read the
	// field from the running service's config via the socket" — the daemon
	// must not need its own copy of config.json.
	MaintainerNpub string
	// Relays the daemon connects to (outbound wss only).
	Relays []string
	// SocketPath of the TollGate service (default /var/run/tollgate.sock).
	SocketPath string
}

// ValidateKeyPair enforces the one hard rule of CVM key configuration: the
// server key and any client key MUST differ. A server that shares a key with
// its client receives its own gift-wrapped requests as if they were inbound
// (echo loop), and response correlation breaks — the failure is silent, which
// is why it is refused here rather than debugged later.
func ValidateKeyPair(serverKeyHex, clientKeyHex string) error {
	if len(serverKeyHex) != 64 {
		return fmt.Errorf("server key must be 64 hex chars, got %d", len(serverKeyHex))
	}
	if serverKeyHex == clientKeyHex {
		return fmt.Errorf("server and client keys are identical — a shared key makes the server receive its own gift-wrapped requests (echo loop)")
	}
	return nil
}

// hexEnv reads an env var that must hold exactly 64 lowercase hex chars.
// A truncated key file is the classic failure (nip19 rejects "unpadded hex of
// length 63"), so the length is checked here, at boot, with a loud message.
func hexEnv(name string) (string, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return "", fmt.Errorf("%s is not set — the CVM daemon needs its own Nostr key (generate one: nak key generate)", name)
	}
	if len(v) != 64 {
		return "", fmt.Errorf("%s must be 64 hex chars, got %d — a truncated key file produces exactly this shape", name, len(v))
	}
	for _, c := range v {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", fmt.Errorf("%s must be lowercase hex", name)
		}
	}
	return v, nil
}

// LoadServerConfig resolves the daemon's runtime configuration. Everything is
// optional except the server key: without it there is nothing to announce and
// nothing to decrypt with, so the daemon refuses to run rather than sitting
// silent on no relays.
func LoadServerConfig() (*ServerConfig, error) {
	key, err := hexEnv("TOLLGATE_CVM_NSEC_HEX")
	if err != nil {
		return nil, err
	}

	relays := []string{"wss://relay2.contextvm.org", "wss://relay.primal.net", "wss://nos.lol"}
	if custom := strings.TrimSpace(os.Getenv("TOLLGATE_CVM_RELAYS")); custom != "" {
		relays = strings.Split(custom, ",")
	}

	socket := "/var/run/tollgate.sock"
	if custom := strings.TrimSpace(os.Getenv("TOLLGATE_CVM_SOCKET")); custom != "" {
		socket = custom
	}
	if dir := strings.TrimSpace(os.Getenv("TOLLGATE_TEST_CONFIG_DIR")); dir != "" {
		socket = dir + "/tollgate.sock"
	}

	return &ServerConfig{
		ServerKeyHex:   key,
		MaintainerNpub: strings.TrimSpace(os.Getenv("TOLLGATE_CVM_MAINTAINER_NPUB")),
		Relays:         relays,
		SocketPath:     socket,
	}, nil
}
