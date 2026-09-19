// lib/boltdump/main.go — read a gonuts wallet bolt database and report where
// the bytes actually are: the logical database size, per-bucket key/value
// counts and byte totals, and (for the proofs bucket) the per-record sizes.
//
// Why this exists: the store is a single bbolt file that is allocated in
// fixed steps (256 KiB on this device) and never shrinks, so `wc -c` on the
// file cannot show growth per payment. bolt's own logical size (tx.Size()) and
// the per-record byte totals can.
//
// Build (its own module, so the parent repo's `go build ./...` skips it):
//
//	cd lib/boltdump && go build -o ../../../raw/boltdump .
//
// Usage:
//
//	boltdump <path-to-wallet.db-copy>
//
// Read-only: opens the copy with bolt.Open(..., ReadOnly: true).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	bolt "go.etcd.io/bbolt"
)

type bucketStat struct {
	Path      string `json:"path"`
	Keys      int    `json:"keys"`
	SubBuckets int   `json:"sub_buckets"`
	ValueBytes int64 `json:"value_bytes"`
	KeyBytes   int64 `json:"key_bytes"`
}

func walk(b *bolt.Bucket, prefix string, out *[]bucketStat) {
	s := bucketStat{Path: prefix}
	b.ForEach(func(k, v []byte) error {
		if v == nil { // sub-bucket
			s.SubBuckets++
			walk(b.Bucket(k), prefix+"/"+string(k), out)
			return nil
		}
		s.Keys++
		s.KeyBytes += int64(len(k))
		s.ValueBytes += int64(len(v))
		return nil
	})
	*out = append(*out, s)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: boltdump <wallet.db> | boltdump --keysets <wallet.db>")
		os.Exit(2)
	}
	if os.Args[1] == "--keysets" {
		keysetsMode(os.Args[2])
		return
	}
	summaryMode(os.Args[1])
}

// keysetsMode prints the per-mint keyset records that matter for the
// "Duplicate outputs" failure mode: the persisted blinding Counter. A counter
// that does not advance across operations (or that resets on restart) makes
// the wallet re-submit already-signed blinded outputs.
func keysetsMode(path string) {
	db, err := bolt.Open(path, 0600, &bolt.Options{ReadOnly: true})
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()
	type rec struct {
		Id          string
		MintURL     string
		Unit        string
		Active      bool
		Counter     uint32
		InputFeePpk uint
	}
	err = db.View(func(tx *bolt.Tx) error {
		top := tx.Bucket([]byte("keysets"))
		if top == nil {
			fmt.Println("no keysets bucket")
			return nil
		}
		return top.ForEach(func(mintURL, v []byte) error {
			if v != nil {
				return nil
			}
			sub := top.Bucket(mintURL)
			return sub.ForEach(func(k, kv []byte) error {
				if kv == nil {
					return nil
				}
				var r rec
				if err := json.Unmarshal(kv, &r); err != nil {
					fmt.Printf("%s %s <unparseable: %v>\n", mintURL, k, err)
					return nil
				}
				fmt.Printf("mint=%s keyset=%s counter=%d active=%t fee_ppk=%d\n",
					mintURL, r.Id, r.Counter, r.Active, r.InputFeePpk)
				return nil
			})
		})
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "view: %v\n", err)
		os.Exit(1)
	}
}

func summaryMode(path string) {
	db, err := bolt.Open(path, 0600, &bolt.Options{ReadOnly: true})
	if err != nil {
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	st := db.Stats()
	if fi, err := os.Stat(path); err == nil {
		fmt.Printf("file_bytes: %d\n", fi.Size())
	}
	fmt.Printf("tx_count: %d\n", st.TxN)
	fmt.Printf("freelist_pages: %d\n", st.FreePageN)
	fmt.Printf("pending_pages: %d\n", st.PendingPageN)

	var logical int64
	err = db.View(func(tx *bolt.Tx) error {
		logical = tx.Size()
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "view: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("logical_bytes: %d\n", logical)

	var stats []bucketStat
	err = db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			walk(b, string(name), &stats)
			return nil
		})
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "walk: %v\n", err)
		os.Exit(1)
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].Path < stats[j].Path })
	fmt.Println("buckets:")
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("  ", "  ")
	enc.Encode(stats)

	// Per-record sizes for the proofs bucket: the real "bytes per stored
	// token" figure. Proofs are JSON-marshalled and keyed by their secret.
	var sizes []int
	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("proofs"))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			if v == nil {
				return nil
			}
			sizes = append(sizes, len(v)+len(k))
			return nil
		})
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "proofs: %v\n", err)
		os.Exit(1)
	}
	sort.Ints(sizes)
	fmt.Printf("proofs: count=%d records_bytes=%s\n", len(sizes), human(sum(sizes)))
	if len(sizes) > 0 {
		fmt.Printf("proof_record_bytes: min=%d median=%d max=%d mean=%.1f\n",
			sizes[0], sizes[len(sizes)/2], sizes[len(sizes)-1],
			float64(sum(sizes))/float64(len(sizes)))
	}
}

func sum(v []int) int {
	t := 0
	for _, x := range v {
		t += x
	}
	return t
}

func human(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.2f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.2f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
