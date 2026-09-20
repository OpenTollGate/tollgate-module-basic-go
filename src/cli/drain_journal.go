package cli

import (
	"encoding/json"
	"fmt"
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
// aggregate CLI response, so a later per-mint failure discarding it — or a
// process crash between the swap and the response — destroys access to the
// funds (issue #375). Journaling immediately after each success closes
// that window; the pending-proof bucket of wallet.db is an independent,
// wallet-internal second copy.
//
// Entries are never removed by the service: tokens are bearer instruments
// and the journal file is 0600 in a 0700 directory. Operators sweep the
// file once the tokens are secured elsewhere.
func drainJournalPath() string {
	if dir := os.Getenv("TOLLGATE_TEST_CONFIG_DIR"); dir != "" {
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
