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

	recovered, spent, failed := 0, 0, 0
	var totalSats uint64
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		status := processLine(line, lineNum, *walletPath, *dryRun, &totalSats)
		switch status {
		case "recovered":
			recovered++
		case "spent":
			spent++
		default:
			failed++
		}
	}

	fmt.Printf("\n%s\n", strings.Repeat("=", 50))
	fmt.Printf("TOTAL: %d tokens | ✅ %d recovered (%d sats) | ⏭️ %d spent | ❌ %d failed\n",
		lineNum, recovered, totalSats, spent, failed)
	fmt.Println(strings.Repeat("=", 50))
}

func processLine(line string, lineNum int, walletPath string, dryRun bool, totalSats *uint64) string {
	parts := strings.SplitN(line, " | ", 4)
	if len(parts) < 3 {
		fmt.Printf("[%3d] ❌ malformed line\n", lineNum)
		return "failed"
	}

	mintURL := strings.TrimSpace(parts[1])
	tokenStr := strings.TrimSpace(parts[2])

	token, err := cashu.DecodeToken(tokenStr)
	if err != nil {
		fmt.Printf("[%3d] ❌ decode failed: %v\n", lineNum, err)
		return "failed"
	}

	proofs := token.Proofs()
	amount := proofs.Amount()

	ys := make([]string, 0, len(proofs))
	for _, p := range proofs {
		y, err := crypto.HashToCurve([]byte(p.Secret))
		if err != nil {
			fmt.Printf("[%3d] ❌ hash_to_curve(%s): %v\n", lineNum, p.Secret[:16], err)
			return "failed"
		}
		ys = append(ys, fmt.Sprintf("%x", y.SerializeCompressed()))
	}

	stateReq := nut07.PostCheckStateRequest{Ys: ys}
	stateResp, err := client.PostCheckProofState(mintURL, stateReq)
	if err != nil {
		fmt.Printf("[%3d] ❌ checkstate: %v\n", lineNum, err)
		return "failed"
	}

	allSpent := true
	for _, s := range stateResp.States {
		if s.State != nut07.Spent {
			allSpent = false
		}
	}

	if allSpent {
		fmt.Printf("[%3d] ⏭️ already spent (%d sats)\n", lineNum, amount)
		return "spent"
	}

	if dryRun {
		fmt.Printf("[%3d] 💰 recoverable (%d sats) — dry-run\n", lineNum, amount)
		return "failed"
	}

	config := wallet.Config{WalletPath: walletPath, CurrentMintURL: mintURL}
	w, err := wallet.LoadWallet(config)
	if err != nil {
		fmt.Printf("[%3d] ❌ wallet init: %v\n", lineNum, err)
		return "failed"
	}

	received, err := w.Receive(token, true)
	if err != nil {
		fmt.Printf("[%3d] ❌ receive: %v\n", lineNum, err)
		return "failed"
	}

	*totalSats += received
	fmt.Printf("[%3d] ✅ recovered %d sats\n", lineNum, received)
	return "recovered"
}
