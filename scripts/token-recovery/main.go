package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/OpenTollGate/gonuts-tollgate/cashu"
	"github.com/OpenTollGate/gonuts-tollgate/cashu/nuts/nut07"
	"github.com/OpenTollGate/gonuts-tollgate/crypto"
	"github.com/OpenTollGate/gonuts-tollgate/wallet"
	"github.com/OpenTollGate/gonuts-tollgate/wallet/client"
)

func main() {
	filePath := flag.String("file", "/etc/tollgate/tokens-to-recover.txt", "Recovery file path")
	walletPath := flag.String("wallet", "/etc/tollgate", "Wallet data directory")
	dryRun := flag.Bool("dry-run", false, "Check states without recovering")
	flag.Parse()

	f, err := os.Open(*filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot open %s: %v\n", *filePath, err)
		os.Exit(1)
	}
	defer f.Close()

	fmt.Printf("Token Recovery Tool — %s\n", time.Now().Format(time.RFC3339))
	fmt.Printf("File: %s | Wallet: %s | Dry-run: %v\n\n", *filePath, *walletPath, *dryRun)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	// One wallet handle for the whole run: LoadWallet locks the bolt
	// store — per-token loads contend. Receive() uses each token's mint.
	var w *wallet.Wallet
	recovered, spent, recoverable, pending, failed := 0, 0, 0, 0, 0
	var totalSats uint64
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		status, walletReady := processLine(line, lineNum, &w, *walletPath, *dryRun, &totalSats)
		if !walletReady {
			break
		}
		switch status {
		case "recovered":
			recovered++
		case "recoverable":
			recoverable++
		case "spent":
			spent++
		case "pending":
			pending++
		default:
			failed++
		}
	}

	fmt.Printf("\n%s\n", strings.Repeat("=", 50))
	fmt.Printf("TOTAL: %d lines | ✅ %d recovered (%d sats) | 💰 %d recoverable | ⏭️ %d spent | ⏳ %d pending | ❌ %d failed\n",
		lineNum, recovered, totalSats, recoverable, spent, pending, failed)
	fmt.Println(strings.Repeat("=", 50))
}

// processLine returns (status, walletReady). walletReady=false means the
// wallet could not be initialized — no point scanning further lines.
func processLine(line string, lineNum int, w **wallet.Wallet, walletPath string, dryRun bool, totalSats *uint64) (string, bool) {
	parts := strings.SplitN(line, " | ", 4)
	if len(parts) < 3 {
		fmt.Printf("[%3d] ❌ malformed line\n", lineNum)
		return "failed", true
	}

	mintURL := strings.TrimSpace(parts[1])
	tokenStr := strings.TrimSpace(parts[2])

	token, err := cashu.DecodeToken(tokenStr)
	if err != nil {
		fmt.Printf("[%3d] ❌ decode failed: %v\n", lineNum, err)
		return "failed", true
	}

	proofs := token.Proofs()
	amount := proofs.Amount()

	ys := make([]string, 0, len(proofs))
	for _, p := range proofs {
		y, err := crypto.HashToCurve([]byte(p.Secret))
		if err != nil {
			// No secret material in output — proofs' secrets spend tokens.
			fmt.Printf("[%3d] ❌ hash_to_curve: %v\n", lineNum, err)
			return "failed", true
		}
		ys = append(ys, fmt.Sprintf("%x", y.SerializeCompressed()))
	}

	var stateResp *nut07.PostCheckStateResponse
	// The client lib takes no context; bound each checkstate call so one
	// hung mint cannot stall the whole recovery run.
	type stateResult struct {
		resp *nut07.PostCheckStateResponse
		err  error
	}
	ch := make(chan stateResult, 1)
	go func() {
		resp, err := client.PostCheckProofState(mintURL, nut07.PostCheckStateRequest{Ys: ys})
		ch <- stateResult{resp, err}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			fmt.Printf("[%3d] ❌ checkstate: %v\n", lineNum, res.err)
			return "failed", true
		}
		stateResp = res.resp
	case <-time.After(30 * time.Second):
		fmt.Printf("[%3d] ❌ checkstate timeout after 30s\n", lineNum)
		return "failed", true
	}

	// Recoverable requires proof-of-unspent: PENDING is in-flight at the
	// mint — receiving now double-spends. Retry later instead.
	allUnspent := true
	anyPending := false
	for _, s := range stateResp.States {
		if s.State == nut07.Pending {
			anyPending = true
		}
		if s.State != nut07.Unspent {
			allUnspent = false
		}
	}

	if allUnspent && dryRun {
		fmt.Printf("[%3d] 💰 recoverable (%d sats) — dry-run\n", lineNum, amount)
		return "recoverable", true
	}
	if anyPending {
		fmt.Printf("[%3d] ⏳ pending at mint (%d sats) — retry later\n", lineNum, amount)
		return "pending", true
	}
	if !allUnspent {
		fmt.Printf("[%3d] ⏭️ already spent (%d sats)\n", lineNum, amount)
		return "spent", true
	}

	if *w == nil {
		config := wallet.Config{WalletPath: walletPath, CurrentMintURL: mintURL}
		loaded, err := wallet.LoadWallet(config)
		if err != nil {
			fmt.Printf("[%3d] ❌ wallet init: %v\n", lineNum, err)
			return "failed", false
		}
		*w = loaded
	}

	received, err := (*w).Receive(token, true)
	if err != nil {
		fmt.Printf("[%3d] ❌ receive: %v\n", lineNum, err)
		return "failed", true
	}

	*totalSats += received
	fmt.Printf("[%3d] ✅ recovered %d sats\n", lineNum, received)
	return "recovered", true
}
