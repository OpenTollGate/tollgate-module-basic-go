package spilmansidecar

// Client for the Spilman protocol sidecar: the Go end of the capabilities-only
// v1 IPC contract. See docs/architecture/spilman-sidecar-decision.md.
//
// # What this client is not
//
// It exposes exactly one operation, `capabilities`, plus the framing and
// correlation rules every future method must obey. It opens no mint
// connection, holds no key, and cannot settle anything. `Capabilities`
// returning success means "a sidecar speaking transport.v1 answered"; it is
// NOT settlement readiness, and callers must gate on an explicit settlement
// capability that this build never advertises.
//
// # Why correlation is mandatory
//
// The existing wallet sidecar transport (src/tollwallet/sidecar.go) sends a
// process-local request id and does not check the id on the response. That is
// tolerable for a balance read and unacceptable for channel state: a reply
// applied to the wrong operation is a wrong settlement. Every reply here must
// echo BOTH ids and the protocol version, or the exchange is discarded.

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
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// ProtocolVersion is the only wire version this client speaks.
const ProtocolVersion = 1

// MaxFrameBytes is the maximum frame size in bytes INCLUDING the newline. The
// sidecar enforces the same bound; this constant must equal it or one side will
// silently reject what the other considers legal.
const MaxFrameBytes = 65536

// MaxIDLength is the longest accepted identifier, in characters.
const MaxIDLength = 128

// CapabilityTransportV1 is the only capability this milestone's sidecar
// advertises. It proves framing and correlation, nothing else.
const CapabilityTransportV1 = "transport.v1"

// methodCapabilities is the only method of protocol version 1.
const methodCapabilities = "capabilities"

// defaultTimeout bounds one exchange when Options.Timeout is unset.
const defaultTimeout = 10 * time.Second

// ErrCode is a refusal code carried on the wire. They are strings so a peer can
// log an unrecognised code instead of losing it to a decode failure.
type ErrCode string

const (
	ErrCodeInvalidRequest        ErrCode = "invalid_request"
	ErrCodeUnsupportedVersion    ErrCode = "unsupported_version"
	ErrCodeUnsupportedCapability ErrCode = "unsupported_capability"
	ErrCodeFrameTooLarge         ErrCode = "frame_too_large"
)

// Client-side failures. Each is a refusal by decision, never something to
// retry automatically.
var (
	// ErrUnsafeEndpoint: the socket path or its directory is not something we
	// will connect to (not a socket, a symlink, wrong owner, group/world
	// accessible parent). Connecting anyway would trust an endpoint another
	// identity controls.
	ErrUnsafeEndpoint = errors.New("spilmansidecar: unsafe socket endpoint")

	// ErrInvalidOperationID: the caller's operation id would not survive the
	// wire contract, so the request is refused BEFORE anything is written.
	ErrInvalidOperationID = errors.New("spilmansidecar: invalid operation id")

	// ErrResponseCorrelation: the reply did not echo this request's ids, so it
	// cannot be attributed to this operation. The connection is abandoned.
	ErrResponseCorrelation = errors.New("spilmansidecar: response does not correlate with the request")

	// ErrResponseVersion: the reply declared a protocol version we do not speak.
	ErrResponseVersion = errors.New("spilmansidecar: unsupported response version")

	// ErrResponseShape: the reply was not exactly one well-formed v1 object.
	ErrResponseShape = errors.New("spilmansidecar: malformed response")

	// ErrFrameTooLarge: the reply exceeded MaxFrameBytes without terminating.
	ErrFrameTooLarge = errors.New("spilmansidecar: response frame exceeds the size bound")

	// ErrExchangeAmbiguous: the request was written and no correlated reply
	// arrived. For a money-moving method this is NEVER a failure signal — the
	// peer may have acted. Callers must reconcile against durable state.
	ErrExchangeAmbiguous = errors.New("spilmansidecar: request sent, no correlated reply; reconcile, do not retry")

	// ErrUnsupportedCapability: the peer refused a method/capability, or the
	// caller required one the peer did not advertise.
	ErrUnsupportedCapability = errors.New("spilmansidecar: capability not supported")
)

// Options configures a client. Zero values are safe.
type Options struct {
	// Timeout bounds dial, write and read of one exchange.
	Timeout time.Duration
	// ExpectedUID is the uid that must own the socket's parent directory.
	// Nil means "this process's effective uid", which is the normal
	// daemon-and-sidecar shape and is a real check, not a skip. A pointer is
	// used because uid 0 (root) is a legitimate expectation and must not be
	// confused with "unset".
	ExpectedUID *int
}

// Client is one connection to the sidecar socket. It is not safe for
// concurrent use: one exchange at a time keeps request/response correlation
// trivially auditable.
type Client struct {
	conn    net.Conn
	rd      *bufio.Reader
	timeout time.Duration
	seq     atomic.Uint64
}

// Dial connects to socketPath after verifying the endpoint.
//
// Verification happens before the connect so a misconfigured or hijacked
// endpoint fails loudly instead of answering.
func Dial(ctx context.Context, socketPath string, opts Options) (*Client, error) {
	if err := verifyEndpoint(socketPath, opts.ExpectedUID); err != nil {
		return nil, err
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("spilmansidecar: dial %s: %w", socketPath, err)
	}
	return &Client{conn: conn, rd: bufio.NewReader(conn), timeout: timeout}, nil
}

// Close releases the connection.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// Capabilities performs the v1 `capabilities` exchange.
//
// operationID identifies the logical operation this handshake belongs to (for
// example the registration a caller is about to attempt). It is validated
// locally; an invalid id never reaches the wire.
func (c *Client) Capabilities(ctx context.Context, operationID string) ([]string, error) {
	requestID, err := c.nextRequestID(operationID)
	if err != nil {
		return nil, err
	}
	response, err := c.exchange(ctx, methodCapabilities, requestID, operationID, nil)
	if err != nil {
		return nil, err
	}
	if response.Error != "" {
		return nil, wireError(response.Error)
	}
	return response.Capabilities, nil
}

// nextRequestID mints a per-exchange id. It must not carry the operation id's
// identity: request_id correlates one round-trip, operation_id identifies the
// operation across round-trips, and conflating them would make a retry look
// like the original exchange.
func (c *Client) nextRequestID(operationID string) (string, error) {
	if err := ValidateID(operationID); err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalidOperationID, err)
	}
	return fmt.Sprintf("req-%d", c.seq.Add(1)), nil
}

type wireRequest struct {
	Version     int             `json:"version"`
	RequestID   string          `json:"request_id"`
	OperationID string          `json:"operation_id"`
	Method      string          `json:"method"`
	Params      json.RawMessage `json:"params,omitempty"`
}

type wireResponse struct {
	Version      int      `json:"version"`
	RequestID    string   `json:"request_id"`
	OperationID  string   `json:"operation_id"`
	Capabilities []string `json:"capabilities"`
	Error        string   `json:"error"`
	// Validated is present ONLY on a successful validate_registration. It stays
	// raw here so each method decodes its own payload with strict field
	// checking instead of one shared struct silently accepting a superset.
	Validated json.RawMessage `json:"validated,omitempty"`
}

// exchange writes one request frame and reads exactly one response frame,
// enforcing version, id correlation and shape. Any violation abandons the
// connection: once framing is in doubt, a later read could return a fragment of
// the previous frame.
func (c *Client) exchange(ctx context.Context, method, requestID, operationID string, params json.RawMessage) (*wireResponse, error) {
	deadline := time.Now().Add(c.timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := c.conn.SetDeadline(deadline); err != nil {
		return nil, err
	}

	body, err := json.Marshal(wireRequest{
		Version:     ProtocolVersion,
		RequestID:   requestID,
		OperationID: operationID,
		Method:      method,
		Params:      params,
	})
	if err != nil {
		return nil, err
	}
	frame := append(body, '\n')
	if len(frame) > MaxFrameBytes {
		return nil, fmt.Errorf("%w: request is %d bytes", ErrFrameTooLarge, len(frame))
	}

	// From here on a failure is ambiguous: the bytes may have reached the peer.
	if _, err := c.conn.Write(frame); err != nil {
		return nil, fmt.Errorf("%w: write failed after the request was built: %v", ErrExchangeAmbiguous, err)
	}
	line, err := c.readFrame()
	if err != nil {
		if errors.Is(err, ErrFrameTooLarge) || errors.Is(err, ErrResponseShape) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrExchangeAmbiguous, err)
	}

	var response wireResponse
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSuffix(string(line), "\n")))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrResponseShape, err)
	}
	if decoder.More() {
		return nil, fmt.Errorf("%w: trailing content after the response object", ErrResponseShape)
	}

	if response.Version != ProtocolVersion {
		return nil, fmt.Errorf("%w: peer answered version %d", ErrResponseVersion, response.Version)
	}
	if response.RequestID != requestID {
		return nil, fmt.Errorf("%w: request_id %q, sent %q", ErrResponseCorrelation, response.RequestID, requestID)
	}
	if response.OperationID != operationID {
		return nil, fmt.Errorf("%w: operation_id %q, sent %q", ErrResponseCorrelation, response.OperationID, operationID)
	}
	return &response, nil
}

// readFrame reads one bounded, newline-terminated frame. It never truncates: a
// reply that grows past MaxFrameBytes is refused while it is still being read,
// so an unterminated flood is reported as a size violation rather than as a
// bare EOF that would look like an ambiguous exchange.
func (c *Client) readFrame() ([]byte, error) {
	frame := make([]byte, 0, 512)
	for {
		chunk, err := c.rd.ReadSlice('\n')
		frame = append(frame, chunk...)
		if len(frame) > MaxFrameBytes {
			return nil, fmt.Errorf("%w: more than %d bytes, no terminator", ErrFrameTooLarge, MaxFrameBytes)
		}
		if err == nil {
			return frame, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return nil, err
	}
}

// wireError maps a refusal code to an error the caller can branch on. An
// unrecognised code keeps its text: the caller needs to see what a newer peer
// said, and must not interpret it as success.
func wireError(code string) error {
	switch ErrCode(code) {
	case ErrCodeUnsupportedCapability:
		return fmt.Errorf("%w: %s", ErrUnsupportedCapability, code)
	case ErrCodeInvalidRequest, ErrCodeUnsupportedVersion, ErrCodeFrameTooLarge:
		return fmt.Errorf("spilmansidecar: peer refused the request: %s", code)
	default:
		return fmt.Errorf("spilmansidecar: peer refused the request: %s", code)
	}
}

// ValidateID reports whether id survives the wire contract: 1..=128 characters,
// ASCII alphanumeric plus '-' and '_'. Byte length equals character length for
// this alphabet, so the check is order-independent.
func ValidateID(id string) error {
	if id == "" {
		return errors.New("identifier is empty")
	}
	if len(id) > MaxIDLength {
		return fmt.Errorf("identifier is %d bytes, limit is %d", len(id), MaxIDLength)
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return fmt.Errorf("identifier contains a disallowed character at byte %d", i)
		}
	}
	return nil
}

// HasSettlementCapability reports whether caps claims settlement. Always false
// for this milestone: no settlement capability is defined, so this function
// exists to make the gate explicit rather than implied by a missing check.
func HasSettlementCapability(caps []string) bool {
	for _, capability := range caps {
		if strings.HasPrefix(capability, "settlement.") {
			return true
		}
	}
	return false
}

// RequireCapability fails unless want is advertised. Callers gate on this
// instead of assuming a handshake success implies a capability.
func RequireCapability(have []string, want string) error {
	for _, capability := range have {
		if capability == want {
			return nil
		}
	}
	return fmt.Errorf("%w: %q not advertised (have %v)", ErrUnsupportedCapability, want, have)
}

// verifyEndpoint refuses paths we must not trust: a symlink (it can be
// repointed at a socket another identity owns), a non-socket, a parent
// directory that grants group/world access, or an unexpected owning uid
// (default: the caller's own effective uid).
func verifyEndpoint(socketPath string, expectedUID *int) error {
	if socketPath == "" {
		return fmt.Errorf("%w: empty path", ErrUnsafeEndpoint)
	}
	info, err := os.Lstat(socketPath)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnsafeEndpoint, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s is a symlink", ErrUnsafeEndpoint, socketPath)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%w: %s is not a socket", ErrUnsafeEndpoint, socketPath)
	}

	dir := filepath.Dir(socketPath)
	dirInfo, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("%w: parent directory: %v", ErrUnsafeEndpoint, err)
	}
	if !dirInfo.IsDir() {
		return fmt.Errorf("%w: parent %s is not a directory", ErrUnsafeEndpoint, dir)
	}
	if mode := dirInfo.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Errorf("%w: parent directory %s has mode %04o; expected 0700 or stricter", ErrUnsafeEndpoint, dir, mode)
	}

	want := os.Geteuid()
	if expectedUID != nil {
		want = *expectedUID
	}
	stat, ok := dirInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%w: cannot read the parent directory's owner", ErrUnsafeEndpoint)
	}
	if int(stat.Uid) != want {
		return fmt.Errorf("%w: parent directory owned by uid %d, expected %d", ErrUnsafeEndpoint, stat.Uid, want)
	}
	return nil
}

var _ io.Reader = (*bufio.Reader)(nil)
