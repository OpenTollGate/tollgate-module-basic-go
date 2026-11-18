package spilmansidecar

// Wire-contract tests for ValidateRegistration. Written before the method
// existed and run against a stub that accepted anything, to observe RED.
//
// The client's job here is narrow, and worth stating because it is easy to
// over-claim: it sends a bounded, versioned request and decodes the reply into
// typed values that came FROM THE SIDECAR. It does not re-validate the
// registration, and must not — the channel id and canonical params are derived
// by the pinned Rust protocol crate, and duplicating that derivation in Go is
// the behavioural duplication the project fences off.
//
// What the client can prove is that a refusal surfaces as a refusal with the
// sidecar's own code intact, and that a success carries the honesty marker
// rather than an implied promise about the mint's funds.

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newShortFixture is newFixture with a SHORT temp directory.
//
// AF_UNIX paths are capped at 108 bytes, and t.TempDir() bakes the test name
// into the path — a table-driven subtest called
// "carries_every_refusal_code/channel_id_not_derivable" overflows it and the
// bind fails with EINVAL. That failure looks like a product bug and is not one,
// so the socket lives in an abbreviated directory instead.
func newShortFixture(t *testing.T, handler func(req map[string]any) []byte) *fixture {
	t.Helper()
	dir, err := os.MkdirTemp("", "reg")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	sock := filepath.Join(dir, "s.sock")
	if len(sock) > 100 {
		t.Fatalf("socket path too long for AF_UNIX: %d bytes: %s", len(sock), sock)
	}
	f := &fixture{t: t, dir: dir, socket: sock, handler: handler}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close(); _ = os.RemoveAll(dir) })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.connects++
			f.mu.Unlock()
			go f.serve(conn)
		}
	}()
	return f
}

// registrationReply builds a well-formed reply carrying a refusal code.
func registrationReply(t *testing.T, req map[string]any, code string) []byte {
	t.Helper()
	resp := map[string]any{
		"version":      float64(ProtocolVersion),
		"request_id":   req["request_id"],
		"operation_id": req["operation_id"],
		"error":        code,
	}
	encoded, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal reply: %v", err)
	}
	return append(encoded, '\n')
}

// registrationSuccess builds a reply with the given validated payload.
func registrationSuccess(t *testing.T, req map[string]any, validated string) []byte {
	t.Helper()
	resp := map[string]any{
		"version":      float64(ProtocolVersion),
		"request_id":   req["request_id"],
		"operation_id": req["operation_id"],
		"error":        "",
		"validated":    json.RawMessage(validated),
	}
	encoded, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal reply: %v", err)
	}
	return append(encoded, '\n')
}

// validatedFixture is a complete, well-formed set of derived values.
func validatedFixture(overrides map[string]any) string {
	payload := map[string]any{
		"channel_id":                    strings.Repeat("ab", 32),
		"canonical_params_json":         `{"unit":"sat"}`,
		"receiver_pubkey":               "02" + strings.Repeat("22", 32),
		"sender_pubkey":                 "03" + strings.Repeat("33", 32),
		"mint":                          "https://mint.example",
		"unit":                          "sat",
		"capacity":                      1000,
		"funding_token_amount":          1000,
		"expiry_timestamp":              2000000000,
		"setup_timestamp":               1700000000,
		"keyset_id":                     "009a1f293253e41e",
		"input_fee_ppk":                 0,
		"maximum_amount_for_one_output": 1000,
		"trust_signal":                  "registration_validated",
		"mint_funds_asserted":           false,
	}
	for k, v := range overrides {
		payload[k] = v
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// validRequest is well-formed enough for the client to send. The client does
// not judge these values, so they only need to be present and bounded.
func validRequest() RegistrationRequest {
	return RegistrationRequest{
		ReceiverSecretHex: strings.Repeat("11", 32),
		TrustedMintURL:    "https://mint.example",
		TrustedKeysetInfo: json.RawMessage(`{"keyset_id":"009a1f293253e41e"}`),
		Registration: Registration{
			SenderPubkey:              "03" + strings.Repeat("33", 32),
			ReceiverPubkey:            "02" + strings.Repeat("22", 32),
			Mint:                      "https://mint.example",
			Unit:                      "sat",
			Capacity:                  1000,
			FundingTokenAmount:        1024,
			ExpiryTimestamp:           2000000000,
			SetupTimestamp:            1700000000,
			KeysetID:                  "009a1f293253e41e",
			InputFeePPK:               0,
			MaximumAmountForOneOutput: 1000,
		},
		ParamsJSON: `{"unit":"sat"}`,
		NowUnix:    1700000000,
	}
}

func TestValidateRegistrationSendsTheContractMethod(t *testing.T) {
	var gotMethod string
	var gotParams map[string]any
	f := newShortFixture(t, func(req map[string]any) []byte {
		if m, ok := req["method"].(string); ok {
			gotMethod = m
		}
		if p, ok := req["params"].(map[string]any); ok {
			gotParams = p
		}
		return registrationReply(t, req, "mint_not_trusted")
	})
	_, _ = f.client(t).ValidateRegistration(context.Background(), "op-1", validRequest())

	if MethodValidateRegistration != "validate_registration" {
		t.Fatalf("the method name is part of the wire contract: %q", MethodValidateRegistration)
	}
	if gotMethod != MethodValidateRegistration {
		t.Fatalf("client sent method %q, want %q", gotMethod, MethodValidateRegistration)
	}
	// The request must be typed and complete: a missing field here is a
	// validation the sidecar cannot perform.
	for _, key := range []string{"receiver_secret_hex", "trusted_mint_url", "trusted_keyset_info", "registration", "params_json", "now_unix"} {
		if _, ok := gotParams[key]; !ok {
			t.Fatalf("request is missing %q: the sidecar cannot validate what was not sent", key)
		}
	}
}

func TestValidateRegistrationRefusalKeepsSidecarCode(t *testing.T) {
	f := newShortFixture(t, func(req map[string]any) []byte {
		return registrationReply(t, req, "mint_not_trusted")
	})

	got, err := f.client(t).ValidateRegistration(context.Background(), "op-1", validRequest())
	if err == nil {
		t.Fatalf("a refusal must not return validated values, got %+v", got)
	}
	var refusal *RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("want *RefusalError, got %T: %v", err, err)
	}
	if refusal.Code != "mint_not_trusted" {
		t.Fatalf("code must be the sidecar's own: got %q", refusal.Code)
	}
	if !errors.Is(err, ErrRegistrationRefused) {
		t.Fatalf("refusal must match ErrRegistrationRefused: %v", err)
	}
}

// Every refusal code the sidecar can emit must survive the trip. A code the
// client rewrites is a code a caller cannot act on, and a caller who cannot
// tell "wrong unit" from "expired" will retry the wrong thing forever.
func TestValidateRegistrationCarriesEveryRefusalCode(t *testing.T) {
	codes := []string{
		"receiver_pubkey_mismatch",
		"nested_params_mismatch",
		"mint_not_trusted",
		"keyset_mismatch",
		"unsupported_unit",
		"zero_capacity",
		"registration_expired",
		"invalid_lifetime",
		"channel_id_not_derivable",
		"sender_equals_receiver",
		"unsupported_capability",
		"invalid_request",
	}
	for _, code := range codes {
		t.Run(code, func(t *testing.T) {
			f := newShortFixture(t, func(req map[string]any) []byte {
				return registrationReply(t, req, code)
			})
			_, err := f.client(t).ValidateRegistration(context.Background(), "op-1", validRequest())
			var refusal *RefusalError
			if !errors.As(err, &refusal) {
				t.Fatalf("want *RefusalError, got %T: %v", err, err)
			}
			if refusal.Code != code {
				t.Fatalf("code rewritten: got %q want %q", refusal.Code, code)
			}
		})
	}
}

func TestValidateRegistrationSuccessCarriesHonestyMarker(t *testing.T) {
	f := newShortFixture(t, func(req map[string]any) []byte {
		return registrationSuccess(t, req, validatedFixture(nil))
	})

	got, err := f.client(t).ValidateRegistration(context.Background(), "op-1", validRequest())
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got.MintFundsAsserted {
		t.Fatal("the client must never present validation as a statement about mint funds")
	}
	if got.TrustSignal != TrustSignalRegistrationValidated {
		t.Fatalf("trust signal must be the sidecar's, got %q", got.TrustSignal)
	}
	if got.ChannelID != strings.Repeat("ab", 32) {
		t.Fatalf("channel id must come from the sidecar, got %q", got.ChannelID)
	}
	if got.Capacity != 1000 {
		t.Fatalf("capacity must come from the sidecar, got %d", got.Capacity)
	}
}

// A success WITHOUT the marker is not reportable: absence would read as "no
// claim made", which in a log is indistinguishable from a claim.
func TestValidateRegistrationRequiresTheHonestyMarker(t *testing.T) {
	payload := validatedFixture(nil)
	var asMap map[string]any
	if err := json.Unmarshal([]byte(payload), &asMap); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	delete(asMap, "mint_funds_asserted")
	stripped, err := json.Marshal(asMap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	f := newShortFixture(t, func(req map[string]any) []byte {
		return registrationSuccess(t, req, string(stripped))
	})
	if _, err := f.client(t).ValidateRegistration(context.Background(), "op-1", validRequest()); !errors.Is(err, ErrResponseShape) {
		t.Fatalf("a success missing mint_funds_asserted must be a shape error, got %v", err)
	}
}

// An empty channel id means the peer reported a channel it did not derive.
func TestValidateRegistrationRejectsEmptyDerivedValues(t *testing.T) {
	for _, field := range []string{"channel_id", "canonical_params_json", "receiver_pubkey", "keyset_id"} {
		t.Run(field, func(t *testing.T) {
			f := newShortFixture(t, func(req map[string]any) []byte {
				return registrationSuccess(t, req, validatedFixture(map[string]any{field: ""}))
			})
			if _, err := f.client(t).ValidateRegistration(context.Background(), "op-1", validRequest()); !errors.Is(err, ErrResponseShape) {
				t.Fatalf("an empty %s must be a shape error, got %v", field, err)
			}
		})
	}
}

// A success response must not be decodable as carrying values it did not send:
// the client checks shape, not content.
func TestValidateRegistrationSuccessWithNoValuesIsAShapeError(t *testing.T) {
	f := newShortFixture(t, func(req map[string]any) []byte {
		return registrationReply(t, req, "")
	})
	if _, err := f.client(t).ValidateRegistration(context.Background(), "op-1", validRequest()); !errors.Is(err, ErrResponseShape) {
		t.Fatalf("want ErrResponseShape for a success with no values, got %v", err)
	}
}

// The request must be bounded before it is written: an oversized params blob
// is refused locally and nothing reaches the socket.
func TestValidateRegistrationRefusesOversizedRequestLocally(t *testing.T) {
	f := newShortFixture(t, func(req map[string]any) []byte {
		return registrationReply(t, req, "")
	})
	req := validRequest()
	req.ParamsJSON = strings.Repeat("x", MaxFrameBytes)

	_, err := f.client(t).ValidateRegistration(context.Background(), "op-1", req)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("want ErrFrameTooLarge, got %v", err)
	}
	if n := f.frameCount(); n != 0 {
		t.Fatalf("an oversized request must not reach the socket, %d frame(s) written", n)
	}
}

func TestValidateRegistrationValidatesOperationID(t *testing.T) {
	f := newShortFixture(t, func(req map[string]any) []byte {
		return registrationReply(t, req, "")
	})
	if _, err := f.client(t).ValidateRegistration(context.Background(), "", validRequest()); !errors.Is(err, ErrInvalidOperationID) {
		t.Fatalf("want ErrInvalidOperationID, got %v", err)
	}
}

// A transport-only build refuses by capability, not by malformed request, so a
// caller can tell "wrong deploy" from "bad input".
func TestValidateRegistrationReportsUnsupportedCapability(t *testing.T) {
	f := newShortFixture(t, func(req map[string]any) []byte {
		return registrationReply(t, req, "unsupported_capability")
	})
	_, err := f.client(t).ValidateRegistration(context.Background(), "op-1", validRequest())
	if !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("want ErrUnsupportedCapability, got %v", err)
	}
	var refusal *RefusalError
	if !errors.As(err, &refusal) || refusal.Code != "unsupported_capability" {
		t.Fatalf("the sidecar's code must survive: %v", err)
	}
}
