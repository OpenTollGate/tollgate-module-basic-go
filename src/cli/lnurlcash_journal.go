package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/paymentjournal"
)

// The LNURLcash rail journals every redemption attempt before the note leaves the
// router, and nothing retires those records on its own. Two states need an operator:
//
//   - `awaiting`: the melt was attempted and the outcome is unknown. The note is
//     reserved on purpose — releasing it would risk melting a note the issuer may
//     already have burned. Only the issuer can say which happened, and Client.Check is
//     the call that asks.
//   - `failed`: the attempt died before the issuer was contacted. The note is free to
//     retry, but the record still consumes a slot the journal never reclaims, which is
//     why the rail bounds retries per note.
//
// Without this command the reconciliation call had no caller at all, so an `awaiting`
// record was a slot the shipped binary could never retire — the bounded-retry fix stops
// one note from exhausting the journal, it does not give an operator a way back.
//
// The journal is opened READ-ONLY here and this command never mutates a record: deciding
// that a note is spent is a judgement about someone's money, and it belongs to whoever
// runs the issuer, not to a `tollgate` invocation.

// LNURLCashJournalEntry is one journal record, rendered for an operator.
type LNURLCashJournalEntry struct {
	ID         string `json:"id"`
	State      string `json:"state"`
	Issuer     string `json:"issuer"`
	AmountMSat uint64 `json:"amount_msat"`
	CreatedAt  string `json:"created_at"`
	// NeedsOperator is the whole point of the command: these are the records whose
	// state cannot resolve itself.
	NeedsOperator bool `json:"needs_operator"`
	// NextStep says what the operator should do, in words rather than a state name.
	NextStep string `json:"next_step,omitempty"`
}

// lnurlCashJournalEntries reads the rail's journal from the config directory and
// describes each redemption attempt. It returns an error only when the journal cannot be
// read at all; a missing journal means the rail has never redeemed anything, which is not
// an error.
func lnurlCashJournalEntries(configPath string) ([]LNURLCashJournalEntry, error) {
	journalDir := filepath.Join(filepath.Dir(configPath), "lnurlcash-journal")
	store, err := paymentjournal.Open(journalDir)
	if err != nil {
		return nil, fmt.Errorf("open the LNURLcash journal at %s: %w", journalDir, err)
	}
	defer func() { _ = store.Close() }()

	ops, err := store.List()
	if err != nil {
		return nil, fmt.Errorf("read the LNURLcash journal: %w", err)
	}

	out := make([]LNURLCashJournalEntry, 0, len(ops))
	for _, op := range ops {
		if op.Direction != paymentjournal.Incoming {
			continue
		}
		if !lnurlcashLooksLikeRailsOperation(op.ID) {
			continue
		}
		e := LNURLCashJournalEntry{
			ID:         op.ID,
			State:      string(op.State),
			Issuer:     op.Recipient,
			AmountMSat: op.AmountMSat,
			CreatedAt:  op.CreatedAt.UTC().Format(time.RFC3339),
		}
		switch op.State {
		case paymentjournal.Awaiting:
			e.NeedsOperator = true
			e.NextStep = "the melt was attempted and the outcome is unknown; the note stays reserved on purpose. Ask the issuer whether this note is spent before doing anything else"
		case paymentjournal.Unknown, paymentjournal.Submitted, paymentjournal.Prepared:
			e.NeedsOperator = true
			e.NextStep = "recorded but never confirmed at the issuer; reconcile before releasing"
		case paymentjournal.Failed:
			e.NextStep = "failed before the issuer was contacted, so nothing was spent; the note may be presented again"
		case paymentjournal.Paid, paymentjournal.GrantPending, paymentjournal.Completed:
			e.NextStep = "settled; access follows the quote as with any payment"
		}
		out = append(out, e)
	}
	return out, nil
}

// lnurlcashLooksLikeRailsOperation reports whether a journal id belongs to this rail.
// Redeem derives every id from "lnurlcash-", so that prefix is the whole test; the
// journal is shared with the Lightning rail and this command must not describe its
// records as LNURLcash ones.
func lnurlcashLooksLikeRailsOperation(id string) bool {
	const prefix = "lnurlcash-"
	return len(id) >= len(prefix) && id[:len(prefix)] == prefix
}

// formatLNURLCashJournal is the human form: one line per record, then a count of the
// ones that need a person. It deliberately leads with those.
func formatLNURLCashJournal(entries []LNURLCashJournalEntry) string {
	needs := 0
	for _, e := range entries {
		if e.NeedsOperator {
			needs++
		}
	}
	if len(entries) == 0 {
		return "no LNURLcash redemptions recorded\n"
	}
	out := fmt.Sprintf("%d redemption(s) recorded, %d needing an operator\n", len(entries), needs)
	for _, e := range entries {
		marker := "  "
		if e.NeedsOperator {
			marker = "! "
		}
		out += fmt.Sprintf("%s%s  %-12s %s  %d msat  %s\n", marker, e.ID, e.State, e.Issuer, e.AmountMSat, e.CreatedAt)
		if e.NextStep != "" {
			out += fmt.Sprintf("    %s\n", e.NextStep)
		}
	}
	return out
}

// lnurlCashJournalJSON is the machine form, for a script or a support ticket.
func lnurlCashJournalJSON(entries []LNURLCashJournalEntry) (string, error) {
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}

// handleLNURLCashCommand lists the rail's redemption attempts.
//
// Usage: tollgate lnurlcash [--json]
//
// It is read-only by design. Retiring an `awaiting` record means declaring that a note
// is spent, and only the issuer can say that; the command tells the operator which
// records need a decision and what to ask, rather than guessing on their behalf.
func (s *CLIServer) handleLNURLCashCommand(args []string, flags map[string]string) CLIResponse {
	asJSON := false
	for _, a := range args {
		if a == "--json" || a == "json" {
			asJSON = true
		}
	}
	if v, ok := flags["json"]; ok && (v == "" || v == "true") {
		asJSON = true
	}

	entries, err := lnurlCashJournalEntries(s.configManager.ConfigFilePath)
	if err != nil {
		return CLIResponse{Success: false, Error: err.Error(), Timestamp: time.Now()}
	}

	if asJSON {
		out, jerr := lnurlCashJournalJSON(entries)
		if jerr != nil {
			return CLIResponse{Success: false, Error: jerr.Error(), Timestamp: time.Now()}
		}
		return CLIResponse{Success: true, Message: out, Timestamp: time.Now()}
	}
	return CLIResponse{Success: true, Message: formatLNURLCashJournal(entries), Timestamp: time.Now()}
}
