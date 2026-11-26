package fips_exit

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/nbd-wtf/go-nostr"
)

// fileStore is a real Store: one file, replaced atomically, with the durability
// steps a router needs (temp in the same directory, fsync the file, rename,
// fsync the directory). The in-memory double cannot prove any of that.
type fileStore struct{ path string }

func (f fileStore) Read() ([]byte, error) {
	b, err := os.ReadFile(f.path)
	if os.IsNotExist(err) {
		return nil, ErrNoState
	}
	return b, err
}

func (f fileStore) WriteAtomic(data []byte) error {
	dir := filepath.Dir(f.path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, f.path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// TestPurchaseOverARealSocket is the end-to-end proof for this file: a real
// listener on a real TCP port, a real HTTP client, real files on disk. The
// httptest tests above exercise the handler; this one exercises the claim that
// a buyer can actually reach an exit and buy.
func TestPurchaseOverARealSocket(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_800_000_000, 0)
	claim, source, buyer := redeemFixture(t, now)

	// The listener binds loopback, and the resolver reports the tunnel source
	// the claim is bound to. On a real exit this resolver would read the
	// accepted connection's peer address; here the point is that it is the only
	// source of truth the handler consults.
	src := source
	svc := NewPurchaseService(PurchaseConfig{
		Now:          func() time.Time { return now },
		ExitKey:      claim.Challenge.ExitKey,
		PriceSats:    2,
		Mode:         BillingBytes,
		Store:        fileStore{path: filepath.Join(dir, "state.json")},
		ClientsStore: fileStore{path: filepath.Join(dir, "clients.conf")},
		Cache:        NewNonceCache(8, now),
		Random:       strings.NewReader(strings.Repeat("\x42", 64)),
		ObservedSource: func(r *http.Request) (string, error) {
			if src == (netip.Addr{}) {
				return "", fmt.Errorf("no source")
			}
			return src.String(), nil
		},
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: svc.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()
	base := "http://" + ln.Addr().String() + "/"

	// GET / must hand the buyer usable terms over the wire.
	resp, err := http.Get(base)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	offerBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d (%s)", resp.StatusCode, offerBody)
	}
	var advert struct {
		Kind int        `json:"kind"`
		Tags [][]string `json:"tags"`
	}
	if err := json.Unmarshal(offerBody, &advert); err != nil {
		t.Fatalf("advertisement over the wire is not JSON: %v", err)
	}
	if advert.Kind != 10021 {
		t.Fatalf("served kind = %d, want 10021", advert.Kind)
	}
	ch, ok := challengeFromHeader(t, resp)
	if !ok {
		t.Fatal("the advertisement carries no challenge over the wire")
	}
	if ch.ExitKey != claim.Challenge.ExitKey {
		t.Fatal("the served challenge does not carry this exit's key")
	}
	if ch.Nonce == claim.Challenge.Nonce {
		t.Fatal("the served challenge reused the fixture nonce")
	}

	// POST / with the fixture's own signed claim: the wire path a phone takes.
	body, _ := json.Marshal(PurchaseRequest{Claim: claim, TTLSeconds: 120, ByteBudget: 4096})
	resp, err = http.Post(base, "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	replyBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST status = %d (%s)", resp.StatusCode, replyBody)
	}
	var reply struct {
		Kind      int    `json:"kind"`
		Allotment int    `json:"allotment"`
		Device    string `json:"device-identifier"`
	}
	if err := json.Unmarshal(replyBody, &reply); err != nil {
		t.Fatalf("reply is not JSON: %v", err)
	}
	if reply.Kind != 1022 || reply.Device != buyer.OuterAddress().String() {
		t.Fatalf("reply does not satisfy the module contract: %s", replyBody)
	}

	// The files must really be on disk, and the clients file must be readable by
	// the exit daemon in the form it expects.
	clientsRaw, err := os.ReadFile(filepath.Join(dir, "clients.conf"))
	if err != nil {
		t.Fatalf("clients file was not written: %v", err)
	}
	if !strings.Contains(string(clientsRaw), buyer.OuterAddress().String()) {
		t.Fatalf("clients file does not name the buyer: %s", clientsRaw)
	}
	stateRaw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("state was not written: %v", err)
	}
	// No temp files may survive: the atomic write must have renamed them away.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("atomic write left a temp file behind: %s", e.Name())
		}
	}

	// Capture the state as the daemon would see it after the good purchase, so
	// the replay can be proved to change nothing. (Revision advances twice on a
	// first purchase by design: Grant advances it, and Apply advances it again
	// because the rendered file differs from the snapshot. What must not happen
	// is a refused replay moving it at all.)
	before, err := LoadState(fileStore{path: filepath.Join(dir, "state.json")})
	if err != nil {
		t.Fatalf("reload before replay: %v", err)
	}
	beforeClients, err := os.ReadFile(filepath.Join(dir, "clients.conf"))
	if err != nil {
		t.Fatalf("clients before replay: %v", err)
	}

	// A replay of the same claim must be refused over the wire too.
	resp, err = http.Post(base, "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("replay POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("replay status = %d, want 409", resp.StatusCode)
	}

	// And the state on disk must still be the good one, not a partial write.
	state, err := LoadState(fileStore{path: filepath.Join(dir, "state.json")})
	if err != nil {
		t.Fatalf("reload state: %v", err)
	}
	if _, ok := state.Entitlement(claim.Buyer); !ok {
		t.Fatalf("entitlement lost after replay refusal: %s", stateRaw)
	}
	if state.Revision != before.Revision {
		t.Fatalf("a refused replay advanced the revision: %d -> %d", before.Revision, state.Revision)
	}
	afterClients, err := os.ReadFile(filepath.Join(dir, "clients.conf"))
	if err != nil {
		t.Fatalf("clients after replay: %v", err)
	}
	if string(afterClients) != string(beforeClients) {
		t.Fatalf("a refused replay rewrote the clients file:\n before %q\n after  %q",
			beforeClients, afterClients)
	}
}

// A buyer whose claim is genuine but whose connection comes from another tunnel
// must be refused over the wire. This is the remote-spoofing case at socket
// level, where the resolver is the only thing standing in the way.
func TestPurchaseOverSocketRefusesForeignTunnel(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_800_000_000, 0)
	claim, _, _ := redeemFixture(t, now)
	foreign := netip.MustParseAddr("fd11:2233:4455:6677::9")

	svc := NewPurchaseService(PurchaseConfig{
		Now:          func() time.Time { return now },
		ExitKey:      claim.Challenge.ExitKey,
		PriceSats:    2,
		Mode:         BillingBytes,
		Store:        fileStore{path: filepath.Join(dir, "state.json")},
		ClientsStore: fileStore{path: filepath.Join(dir, "clients.conf")},
		Cache:        NewNonceCache(8, now),
		Random:       strings.NewReader(strings.Repeat("\x42", 64)),
		ObservedSource: func(*http.Request) (string, error) {
			return foreign.String(), nil
		},
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: svc.Handler()}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	body, _ := json.Marshal(PurchaseRequest{Claim: claim, TTLSeconds: 60, ByteBudget: 1000})
	resp, err := http.Post("http://"+ln.Addr().String()+"/", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(dir, "clients.conf")); !os.IsNotExist(err) {
		t.Fatal("a foreign-tunnel purchase wrote a clients file")
	}
}

// Keep the hex import honest: a signature in the clear helps debugging a failure.
var _ = hex.EncodeToString
var _ = schnorr.Sign
var _ = btcec.PrivKeyFromBytes
var _ = nostr.GetPublicKey
