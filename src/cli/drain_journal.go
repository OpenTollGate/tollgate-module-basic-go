package cli

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// Per-mint drain journal: an append-only, fsync'd record of every Cashu
// token successfully produced by a wallet drain, written before the next
// mint is attempted.
//
// Cashu swaps are irreversible once the mint accepts them (NUT-03): the
// token returned by DrainMint is the only spendable representation of
// those funds. Without the journal, that token exists solely in the
// aggregate CLI response, so a later per-mint failure discarding it
// destroys access to the funds (issue #375). Journaling immediately
// after each success keeps an independent second copy for exactly that
// case.
//
// What the journal is NOT: a crash-recovery mechanism. A crash inside
// the swap itself — after the mint accepts the inputs but before the
// token returns — leaves the proofs in wallet.db's pending bucket, and
// recovery from there is scripts/token-recovery's job, not this file's.
// The journal only covers tokens the CLI already holds.
//
// Entries are never removed by the service: tokens are bearer instruments
// and the journal file is 0600 in a 0700 directory. Operators sweep the
// file once the tokens are secured elsewhere.
func drainJournalPath() string {
	// TOLLGATE_TEST_CONFIG_DIR exists for the test harness only; in
	// production the journal always lives under /etc/tollgate.
	if dir := os.Getenv("TOLLGATE_TEST_CONFIG_DIR"); dir != "" {
		// Bearer tokens land wherever this points (#443): say so on every
		// honor so a stray service drop-in or profile export is visible in
		// logread instead of silently splitting state.
		log.Printf("WARNING: TOLLGATE_TEST_CONFIG_DIR is set — drain journal (bearer tokens) redirected to %s", dir)
		return filepath.Join(dir, "wallet-drain-journal.jsonl")
	}
	return filepath.Join("/etc/tollgate", "wallet-drain-journal.jsonl")
}

type drainJournalEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	MintURL    string    `json:"mint_url"`
	AmountSats uint64    `json:"amount_sats"`
	Token      string    `json:"token"`
}

func appendDrainJournal(mintURL string, amountSats uint64, token string) error {
	path := drainJournalPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create drain journal directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open drain journal: %w", err)
	}
	defer file.Close()

	line, err := json.Marshal(drainJournalEntry{
		Timestamp:  time.Now().UTC(),
		MintURL:    mintURL,
		AmountSats: amountSats,
		Token:      token,
	})
	if err != nil {
		return fmt.Errorf("encode drain journal entry: %w", err)
	}
	line = append(line, '\n')

	if _, err := file.Write(line); err != nil {
		return fmt.Errorf("write drain journal: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync drain journal: %w", err)
	}
	return nil
}
