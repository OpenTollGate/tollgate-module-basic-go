// Command lud25d is the LUD-25 mint SERVICE for TollGate.
//
// It issues bearer notes backed by a Cashu mint as Lightning backend.
// k1 is a CSPRNG-generated bearer secret, NOT the Lightning preimage —
// the Cashu mint handles Lightning invoices via NUT-04 quotes.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/cmd/lud25d/internal/mint"
)

func main() {
	var (
		addr     = flag.String("addr", ":8080", "listen address")
		dbPath   = flag.String("db", "lud25d.db", "SQLite database path")
		mintURL  = flag.String("mint-url", "", "Cashu mint URL (required)")
		expiryD  = flag.Duration("expiry", mint.DefaultExpiry, "note expiry duration")
	)
	flag.Parse()

	if *mintURL == "" {
		fmt.Fprintln(os.Stderr, "error: --mint-url is required")
		os.Exit(1)
	}

	db, err := mint.OpenDB(*dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	backend := mint.NewCashuBackend(db, *mintURL, *expiryD)

	// Stub endpoints — full implementation in Task C2
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"tag":"withdrawRequest","status":"ok"}`)
	})

	// /mint — create a new minting invoice (stub for C1)
	http.HandleFunc("/mint", func(w http.ResponseWriter, r *http.Request) {
		amountStr := r.URL.Query().Get("amount")
		var amount int64
		fmt.Sscanf(amountStr, "%d", &amount)
		if amount <= 0 {
			http.Error(w, "invalid amount", http.StatusBadRequest)
			return
		}

		k1, bolt11, _, err := backend.CreateMintingInvoice(amount)
		if err != nil {
			log.Printf("CreateMintingInvoice error: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		log.Printf("created invoice k1=%s... (amount=%d)", k1[:8], amount)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"k1":"%s","pr":"%s"}`, k1, bolt11)
	})

	// /check — check payment status (stub for C1)
	http.HandleFunc("/check", func(w http.ResponseWriter, r *http.Request) {
		k1 := r.URL.Query().Get("k1")
		if k1 == "" {
			http.Error(w, "missing k1", http.StatusBadRequest)
			return
		}

		paid, err := backend.CheckPayment(k1)
		if err != nil {
			log.Printf("CheckPayment error: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"paid":%t}`, paid)
	})

	log.Printf("lud25d listening on %s (mint=%s, expiry=%s)", *addr, *mintURL, *expiryD)
	if err := http.ListenAndServe(*addr, nil); err != nil {
		log.Fatalf("server error: %v", err)
	}

}
