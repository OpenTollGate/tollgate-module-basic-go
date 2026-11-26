// Command exitserver runs the real fips_exit purchase surface so the Android app
// can buy against it.
//
// This exists because every earlier Android purchase success was against a
// Python stand-in that I wrote to imitate the module's contract. A stand-in
// agreeing with my reading of the contract proves nothing about the real code.
// This binary serves the actual fips_exit.PurchaseService — the same handler,
// the same Redeem, the same claim verification — so what the phone exercises is
// the code that would run on an exit.
//
// It is a harness, not a daemon: it hard-codes one exit key, one clock, and a
// directory for state, all from flags, so a run is reproducible and its evidence
// is checkable.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/fips_exit"
)

// fileStore is the same store shape a router needs: temp file in the same
// directory, fsync, rename, fsync the directory.
type fileStore struct{ path string }

func (f fileStore) Read() ([]byte, error) {
	b, err := os.ReadFile(f.path)
	if os.IsNotExist(err) {
		return nil, fips_exit.ErrNoState
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
	defer os.Remove(name)
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

func main() {
	listen := flag.String("listen", "127.0.0.1:2121", "address to listen on")
	dir := flag.String("dir", "", "directory for state and clients file (required)")
	exitKeyHex := flag.String("exit-key", "", "64-char hex x-only key for this exit; random if empty")
	priceSats := flag.Uint64("price", 2, "price in sats per step")
	mint := flag.String("mint", "", "mint URL this exit accepts (advertised to buyers)")
	stepBytes := flag.Uint64("step-bytes", 60000, "bytes bought per priced step")
	flag.Parse()

	if *dir == "" {
		log.Fatal("-dir is required")
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		log.Fatalf("create state dir: %v", err)
	}

	var key fips_exit.PeerKey
	if *exitKeyHex == "" {
		if _, err := rand.Read(key[:]); err != nil {
			log.Fatalf("random exit key: %v", err)
		}
	} else {
		raw, err := hex.DecodeString(*exitKeyHex)
		if err != nil || len(raw) != 32 {
			log.Fatalf("-exit-key must be 64 hex characters")
		}
		copy(key[:], raw)
	}

	svc := fips_exit.NewPurchaseService(fips_exit.PurchaseConfig{
		Now:          time.Now,
		ExitKey:      key,
		PriceSats:    *priceSats,
		StepBytes:    *stepBytes,
		MintURL:      *mint,
		Mode:         fips_exit.BillingBytes,
		Store:        fileStore{path: filepath.Join(*dir, "state.json")},
		ClientsStore: fileStore{path: filepath.Join(*dir, "clients.conf")},
		Cache:        fips_exit.NewNonceCache(1024, time.Now()),
		// The emulator reaches the host through 10.0.2.2, so the accepted
		// connection's peer address is loopback, never the peer's tunnel. On a
		// real exit this resolver reads the accepted connection's source on the
		// FIPS interface, which RFC 2473 preserves. Here it reports the buyer's
		// derived outer address only when the caller asked for it explicitly with
		// -bind-to, so the harness cannot silently pretend to have a tunnel.
		ObservedSource: func(r *http.Request) (string, error) {
			if bindTo := r.URL.Query().Get("bind"); bindTo != "" {
				return bindTo, nil
			}
			return "", fmt.Errorf("no FIPS source binding on this listener")
		},
	})

	log.Printf("exit key %s", key.Hex())
	log.Printf("listening on %s, state in %s", *listen, *dir)
	if err := http.ListenAndServe(*listen, svc.Handler()); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
