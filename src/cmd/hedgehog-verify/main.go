// Command hedgehog-verify verifies a Hedgehog channel spend LOCALLY.
//
// WHY THIS EXISTS AS A SEPARATE BINARY
//
// The router has no Bitcoin node, no chain state and very little spare flash, but
// it can still verify a spend: verification is pure script evaluation. Given the
// transaction, the output it spends, and the witness, the answer is yes or no --
// no node, no network, no chain view required.
//
// That makes this the router-side artefact. It is deliberately small and has no
// dependencies beyond the standard library and the module's own package, because
// it has to fit on a router.
//
// INPUT is JSON on stdin or as a file argument, because the router's serial
// console wraps long command lines at roughly 80 characters -- so a verifier that
// took arguments on the command line would be unusable over serial. A file, or a
// pipe, avoids that entirely.
//
//	{
//	  "tx_hex":       "0200000001...",   // the spending transaction, no witness
//	  "vout":         0,                  // which input to check
//	  "prev_value":   100000000,          // the output being spent
//	  "prev_script":  "5120a710...",      // its scriptPubKey
//	  "witness":      ["<sig>", "<sig>", "<leaf>", "<ctrl>"]
//	}
//
// OUTPUT is JSON on stdout, and the exit code is meaningful: 0 = valid, 1 = not
// valid, 2 = malformed input. A lane can therefore check either.
package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/OpenTollGate/tollgate-module-basic-go/hedgehog"
	"github.com/btcsuite/btcd/wire"
)

type request struct {
	TxHex      string   `json:"tx_hex"`
	Vout       uint32   `json:"vout"`
	PrevValue  int64    `json:"prev_value"`
	PrevScript string   `json:"prev_script"`
	Witness    []string `json:"witness"`
	// ExpectFailure inverts the sense: the verifier reports success when the
	// spend is REJECTED. The lane uses this to assert the safety property that a
	// payer's half cannot spend alone, which is a negative test.
	ExpectFailure bool `json:"expect_failure"`
}

type response struct {
	Valid      bool   `json:"valid"`
	Reason     string `json:"reason,omitempty"`
	WitnessLen int    `json:"witness_len"`
	Error      string `json:"error,omitempty"`
}

func main() {
	os.Exit(run())
}

func run() int {
	var raw []byte
	var err error
	if len(os.Args) > 1 && os.Args[1] != "-" {
		raw, err = os.ReadFile(os.Args[1])
	} else {
		raw, err = io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
	}
	if err != nil {
		return emit(response{Error: fmt.Sprintf("read input: %v", err)}, 2)
	}

	var req request
	if err := json.Unmarshal(raw, &req); err != nil {
		return emit(response{Error: fmt.Sprintf("parse input: %v", err)}, 2)
	}
	resp, code := verify(req)
	return emit(resp, code)
}

func emit(r response, code int) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(r)
	return code
}

func verify(req request) (response, int) {
	resp := response{WitnessLen: len(req.Witness)}

	txBytes, err := hex.DecodeString(req.TxHex)
	if err != nil {
		resp.Error = fmt.Sprintf("tx_hex is not hex: %v", err)
		return resp, 2
	}
	prevScript, err := hex.DecodeString(req.PrevScript)
	if err != nil {
		resp.Error = fmt.Sprintf("prev_script is not hex: %v", err)
		return resp, 2
	}

	var tx wire.MsgTx
	if err := tx.Deserialize(bytesReader(txBytes)); err != nil {
		resp.Error = fmt.Sprintf("tx_hex is not a transaction: %v", err)
		return resp, 2
	}
	if int(req.Vout) >= len(tx.TxIn) {
		resp.Error = fmt.Sprintf("vout %d out of range (%d inputs)",
			req.Vout, len(tx.TxIn))
		return resp, 2
	}

	witness := make([][]byte, 0, len(req.Witness))
	for i, w := range req.Witness {
		b, err := hex.DecodeString(w)
		if err != nil {
			resp.Error = fmt.Sprintf("witness[%d] is not hex: %v", i, err)
			return resp, 2
		}
		witness = append(witness, b)
	}

	prevOut := wire.NewTxOut(req.PrevValue, prevScript)
	err = hedgehog.VerifyScriptPath(&tx, int(req.Vout), prevOut, witness)

	valid := err == nil
	resp.Valid = valid
	if err != nil {
		resp.Reason = err.Error()
	}

	// Exit code reflects the ASSERTION the caller asked for, so a lane can treat
	// a negative test as a pass without parsing JSON.
	if req.ExpectFailure {
		if valid {
			resp.Reason = "spend was ACCEPTED but was expected to fail"
			return resp, 1
		}
		return resp, 0
	}
	if !valid {
		return resp, 1
	}
	return resp, 0
}

// bytesReader avoids importing bytes just for one call, keeping the binary's
// dependency surface minimal.
type byteReader struct {
	b []byte
	i int
}

func bytesReader(b []byte) *byteReader { return &byteReader{b: b} }

func (r *byteReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}

// Compile-time guard: errors is used by VerifyScriptPath's contract; keep the
// import honest if that ever changes.
var _ = errors.New
