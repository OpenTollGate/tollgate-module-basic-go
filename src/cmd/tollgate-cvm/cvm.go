package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip44"
	"github.com/nbd-wtf/go-nostr/nip59"
)

// CVM wire protocol (see ADR D2): MCP JSON-RPC 2.0 messages ride inside
// kind-25910 rumors, gift-wrapped (NIP-59, kind 1059) so only the recipient
// can read them. Kind 25910 is ephemeral; discovery is CEP-6 announcements,
// which is a separate file (announce.go).

// CVMKind is the kind CVM reserves for all message traffic.
const CVMKind = 25910

// JSONRPCRequest is a JSON-RPC 2.0 request or notification.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse is a JSON-RPC 2.0 response (result xor error).
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is the JSON-RPC error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Standard-ish JSON-RPC error codes used by MCP servers.
const (
	ErrParse    = -32700
	ErrInvalid  = -32600
	ErrMethodNF = -32601
	ErrParams   = -32602
	ErrInternal = -32603
	ErrAuthz    = -32000 // authorization refusals (ours)
)

func encryptTo(sk, recipientPub string) func(string) (string, error) {
	return func(plaintext string) (string, error) {
		ck, err := nip44.GenerateConversationKey(recipientPub, sk)
		if err != nil {
			return "", fmt.Errorf("conversation key: %w", err)
		}
		return nip44.Encrypt(plaintext, ck)
	}
}

func decryptWith(sk string) func(string, string) (string, error) {
	// The other pubkey MUST come from the argument, not a captured value:
	// GiftUnwrap passes the gift wrap's nonce pubkey for the outer layer and
	// the seal's (real sender's) pubkey for the inner rumor — two different
	// parties, and capturing either one breaks the other layer.
	return func(otherPub, ciphertext string) (string, error) {
		ck, err := nip44.GenerateConversationKey(otherPub, sk)
		if err != nil {
			return "", fmt.Errorf("conversation key: %w", err)
		}
		return nip44.Decrypt(ciphertext, ck)
	}
}

func signWith(sk string) func(*nostr.Event) error {
	return func(e *nostr.Event) error { return e.Sign(sk) }
}

// WrapRequest builds a gift-wrapped kind-25910 request from callerSK to the
// server at serverPK. The returned caller string is the caller's pubkey —
// the identity the server will authorize.
func WrapRequest(req JSONRPCRequest, serverPK, callerSK string) (nostr.Event, string, error) {
	callerPK, err := nostr.GetPublicKey(callerSK)
	if err != nil {
		return nostr.Event{}, "", fmt.Errorf("caller pubkey: %w", err)
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nostr.Event{}, "", fmt.Errorf("marshal request: %w", err)
	}
	rumor := nostr.Event{
		Kind:      CVMKind,
		Content:   string(payload),
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{nostr.Tag{"p", serverPK}},
	}
	gw, err := nip59.GiftWrap(rumor, serverPK, encryptTo(callerSK, serverPK), signWith(callerSK), nil)
	if err != nil {
		return nostr.Event{}, "", fmt.Errorf("gift wrap: %w", err)
	}
	return gw, callerPK, nil
}

// UnwrapRequest opens a gift-wrapped request with the server's key and
// returns the JSON-RPC request plus the caller's identity (from the seal's
// pubkey — the gift wrap's own pubkey is a nonce and means nothing).
func UnwrapRequest(gw nostr.Event, serverSK string) (JSONRPCRequest, string, error) {
	var req JSONRPCRequest
	rumor, err := nip59.GiftUnwrap(gw, decryptWith(serverSK))
	if err != nil {
		return req, "", fmt.Errorf("unwrap: %w", err)
	}
	// The caller is the seal's author; GiftUnwrap copies it into rumor.PubKey.
	if rumor.Kind != CVMKind {
		return req, "", fmt.Errorf("rumor kind %d, want %d — not a CVM message", rumor.Kind, CVMKind)
	}
	if err := json.Unmarshal([]byte(rumor.Content), &req); err != nil {
		return req, "", fmt.Errorf("request json: %w", err)
	}
	return req, rumor.PubKey, nil
}

// WrapResponse gift-wraps a JSON-RPC response from the server to a client.
// extraTags (e.g. the e-tag correlation marker) go on the GIFT WRAP and must
// be passed here — appending a tag after GiftWrap has signed invalidates the
// signature and relays drop the event silently.
func WrapResponse(resp JSONRPCResponse, clientPK, serverSK string, extraTags ...nostr.Tag) (nostr.Event, error) {
	payload, err := json.Marshal(resp)
	if err != nil {
		return nostr.Event{}, fmt.Errorf("marshal response: %w", err)
	}
	rumor := nostr.Event{
		Kind:      CVMKind,
		Content:   string(payload),
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{nostr.Tag{"p", clientPK}},
	}
	modify := func(gw *nostr.Event) {
		if len(extraTags) > 0 {
			gw.Tags = append(gw.Tags, extraTags...)
		}
	}
	return nip59.GiftWrap(rumor, clientPK, encryptTo(serverSK, clientPK), signWith(serverSK), modify)
}

// UnwrapResponse opens a wrapped response with the client's key.
func UnwrapResponse(gw nostr.Event, clientSK string) (JSONRPCResponse, error) {
	var resp JSONRPCResponse
	rumor, err := nip59.GiftUnwrap(gw, decryptWith(clientSK))
	if err != nil {
		return resp, fmt.Errorf("unwrap: %w", err)
	}
	if rumor.Kind != CVMKind {
		return resp, fmt.Errorf("rumor kind %d, want %d", rumor.Kind, CVMKind)
	}
	if err := json.Unmarshal([]byte(rumor.Content), &resp); err != nil {
		return resp, fmt.Errorf("response json: %w", err)
	}
	return resp, nil
}

// RequireMaintainer is the claim gate on the control plane as a whole.
//
// An unclaimed router (empty maintainer_npub) must NOT serve: with no
// maintainer tag nobody can discover it legitimately, and while the
// Authorizer would still refuse writes, READS would answer any signed caller
// — leaking status, wallet balance and configuration to the world. Refusing
// to serve is the safe default; the refusal names the exact fix.
func RequireMaintainer(npub string) error {
	if strings.TrimSpace(npub) == "" {
		return fmt.Errorf("router is unclaimed: set maintainer_npub first (tollgate config set maintainer_npub <npub1…>), or it will not expose reads to unauthenticated callers")
	}
	return nil
}

// SKFromNpub is deliberately NOT implemented to return a key: an npub is a
// public key and cannot yield its secret. It exists so callers that hold only
// an npub fail loudly instead of passing it where a secret belongs.
func SKFromNpub(npub string) (string, error) {
	return "", fmt.Errorf("an npub is a PUBLIC key — no secret can be derived from %q; provide the nsec/key file instead", npub)
}
