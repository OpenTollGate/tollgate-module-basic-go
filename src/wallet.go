package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"encoding/base64"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip60"
	"sync"
)

var payoutPubkey = "bbb5dda0e15567979f0543407bdc2033d6f0bbb30f72512a981cfdb2f09e2747"
var developerSupportPubkey = "9f4b342eaa7d3e4cc0a1078df9ceda9d4a667edfe3493237b54864b74ee9c9da"
var CombinedPayout = "CombinedPayout"

func init() {
	// Configure custom DNS resolver to bypass local DNS issues
	// This helps with relay connectivity problems
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		DualStack: true,
		Resolver: &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{
					Timeout: 10 * time.Second,
				}
				// Use Google's public DNS
				return d.DialContext(ctx, "udp", "8.8.8.8:53")
			},
		},
	}

	// Apply this dialer to the HTTP client used by the websocket connections
	http.DefaultTransport.(*http.Transport).DialContext = dialer.DialContext
}

// SimpleKeyer is a minimal implementation of the nostr.Keyer interface
type SimpleKeyer struct {
	privateKey string
	publicKey  string
}

func (k *SimpleKeyer) Key() string {
	return k.privateKey
}

func (k *SimpleKeyer) GetPublicKey(ctx context.Context) (string, error) {
	return k.publicKey, nil
}

func (k *SimpleKeyer) Sign(e *nostr.Event) error {
	return e.Sign(k.privateKey)
}

func (k *SimpleKeyer) SignEvent(ctx context.Context, e *nostr.Event) error {
	return e.Sign(k.privateKey)
}

func (k *SimpleKeyer) Encrypt(ctx context.Context, pubkey, plaintext string) (string, error) {
	// Simple base64 encoding as a placeholder for real encryption
	return base64.StdEncoding.EncodeToString([]byte(plaintext)), nil
}

func (k *SimpleKeyer) Decrypt(ctx context.Context, pubkey, ciphertext string) (string, error) {
	// Simple base64 decoding as a placeholder for real decryption
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// decodeCashuToken decodes a Cashu token and returns the total value in sats
func DecodeCashuToken(token string) (int, string, error) {
	fmt.Println("Decoding Cashu token:", token)

	// Only support cashuB tokens
	if !strings.HasPrefix(strings.ToLower(token), "cashub") {
		return 0, "", fmt.Errorf("only cashuB tokens are supported")
	}

	// Try to decode token and get proofs and mint
	proofs, mint, err := nip60.GetProofsAndMint(token)
	if err != nil {
		// Fall back to basic token parsing if there's an error
		log.Printf("Failed to use nip60 to decode token: %v, using fallback", err)

		var amount uint64
		for _, proof := range proofs {
			amount += proof.Amount
		}
		return int(amount), mint, nil
	}

	// Sum up the token amount
	var amount uint64
	for _, proof := range proofs {
		amount += proof.Amount
	}

	return int(amount), mint, nil
}

// CollectPayment processes a Cashu token and swaps it for fresh proofs
// Returns the fresh proofs and token directly
func CollectPayment(token string, privateKey string, relayPool *nostr.SimplePool, relays []string, acceptedMint string) error {
	// Extract proofs from token and process them
	proofs, tokenMint, err := nip60.GetProofsAndMint(token)
	if err != nil {
		log.Printf("Failed to decode token for swapping: %v", err)
		return err
	}

	log.Printf("Successfully decoded token from mint %s", tokenMint)
	log.Printf("Comparing with accepted mint: %s", acceptedMint)

	if !strings.EqualFold(tokenMint, acceptedMint) {
		return fmt.Errorf("token mint %s is not accepted", tokenMint)
	}

	// Get a temporary context for the swap operation
	swapCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create keyer using the tollgate private key
	pubkey, _ := nostr.GetPublicKey(privateKey)
	event := &nostr.Event{ID: "", PubKey: pubkey}
	err = event.Sign(privateKey)

	if err != nil {
		log.Printf("Could not create keyer for token swap: %v", err)
		return err
	}

	// We're using direct mint operations since wallet requires complex keyer
	// Get the current amount
	swapAmount := uint64(0)
	for _, proof := range proofs {
		swapAmount += proof.Amount
	}

	// Only proceed if we have a valid amount
	if swapAmount <= 0 {
		err := fmt.Errorf("token has zero value, not swapping")
		log.Printf("%v", err)
		return err
	}

	log.Printf("Swapping %d sats in proofs for fresh proofs", swapAmount)

	// Create a simple keyer that wraps the private key
	simpleKeyer := &SimpleKeyer{
		privateKey: privateKey,
		publicKey:  pubkey,
	}

	// Create a fresh relay pool specifically for token operations
	// This ensures we have full write capabilities
	// relays = config.Relays

	// Create a new relay pool
	freshPool := nostr.NewSimplePool(swapCtx)

	log.Printf("Relays: %v", relays)

	var wg sync.WaitGroup
	var relayMutex sync.Mutex
	var connectedRelaysList []string

	// Ensure at least one relay is connected
	// TODO: shouldn't these connections be managed by config_manager.go instead?
	connectedRelays := 0
	for _, relay := range relays {
		wg.Add(1)
		go func(relay string) {
			defer wg.Done()
			delay := 1 * time.Second
			for attempt := 0; attempt < 2; attempt++ { // Try up to 5 times
				log.Printf("Attempting to connect to relay %s (attempt %d)", relay, attempt+1)
				_, err := freshPool.EnsureRelay(relay)
				relayMutex.Lock()
				if err == nil {
					connectedRelays++
					connectedRelaysList = append(connectedRelaysList, relay)
					log.Printf("Successfully connected to relay: %s", relay)
				} else {
					log.Printf("Failed to connect to relay %s (attempt %d): %v", relay, attempt+1, err)
				}
				relayMutex.Unlock()
				if err == nil {
					break
				}
				time.Sleep(delay)
				delay *= 2 // Exponential backoff
			}
		}(relay)
	}

	wg.Wait()

	if connectedRelays == 0 {
		return fmt.Errorf("failed to connect to any relays")
	}

	log.Printf("Connected to %d relays successfully: %v", connectedRelays, connectedRelaysList)

	// Create a wallet just for swapping these proofs
	wallet := nip60.LoadWallet(
		swapCtx,
		simpleKeyer,
		freshPool,
		connectedRelaysList,
	)

	wallet.PublishUpdate = func(event nostr.Event, deleted *nip60.Token, received *nip60.Token, change *nip60.Token, isHistory bool) {
		log.Printf("PublishUpdate: %v", event)
	}

	if wallet == nil {
		err := fmt.Errorf("failed to create wallet")
		return err
	}

	// First receive the token
	log.Printf("Receiving proofs for mint %s", tokenMint)
	receiveErr := wallet.Receive(swapCtx, proofs, tokenMint)
	if receiveErr != nil {
		log.Printf("Failed to receive proofs in wallet: %v", receiveErr)
		return receiveErr
	}

	log.Printf("Successfully received proofs, now swapping for fresh ones, balance: %d", wallet.Balance())

	balance := wallet.Balance()
	mintFee, err := config_manager.GetMintFee(tokenMint)
	if err != nil {
		log.Printf("Error getting mint fee for %s: %v", tokenMint, err)
		// Handle the error accordingly
	}
	payoutErr := Payout(CombinedPayout, int(balance), wallet, swapCtx, mintFee)
	if payoutErr != nil {
		log.Printf("Failed to payout profit payout: %v", payoutErr)
		return payoutErr
	}

	return nil
}

func Payout(address string, amount int, wallet *nip60.Wallet, swapCtx context.Context, mintFee int) error {
	log.Printf("Paying out %d sats to %s", amount, address)

	// Skip processing if amount is zero
	if amount <= 0 {
		log.Printf("Skipping payout of zero amount to %s", address)
		return nil
	}

	// Then swap for fresh proofs - use SendExternal to send to ourselves
	freshProofs, tokenMint, swapErr := wallet.Send(swapCtx, uint64(amount)-uint64(mintFee))
	if swapErr != nil {
		log.Printf("Failed to swap proofs: %v", swapErr)
		if len(freshProofs) == 0 {
			log.Printf("WARNING: No proofs generated, possibly due to small amount (%d sats)", amount)
			// Try again without fee
			freshProofs, tokenMint, swapErr = wallet.Send(swapCtx, uint64(amount))
			if swapErr != nil || len(freshProofs) == 0 {
				log.Printf("Failed on retry: %v", swapErr)
				return fmt.Errorf("failed to generate valid proofs for small amount: %v", swapErr)
			}
		}
		return swapErr
	}

	log.Printf("Successfully swapped for fresh proofs, new proofs: %+v", freshProofs)

	// Create a token with the fresh proofs
	freshToken := nip60.MakeTokenString(freshProofs, tokenMint)
	log.Printf("Successfully swapped for fresh proofs, new token: %s", freshToken)

	// Define a persistent storage directory with debug output
	storageDir := "/etc/tollgate/ecash"
	log.Printf("DEBUG: Using storage directory: %s", storageDir)

	// Log the current working directory
	cwd, _ := os.Getwd()
	log.Printf("DEBUG: Current working directory: %s", cwd)

	// Create the storage directory if it doesn't exist
	if err := os.MkdirAll(storageDir, 0777); err != nil { // Fixed comparison with nil
		log.Printf("ERROR: Failed to create storage directory %s: %v", storageDir, err)
		return err
	}
	log.Printf("DEBUG: Storage directory created/verified")

	// Use an absolute path for the token file
	tokenPath := fmt.Sprintf("%s/%s", storageDir, address)
	log.Printf("DEBUG: Will write token to: %s", tokenPath)

	// Write token to a file with more verbose error handling
	file, err := os.OpenFile(tokenPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		log.Printf("ERROR: Failed to open file %s: %v", tokenPath, err)
		// Try alternative location as fallback
		fallbackPath := fmt.Sprintf("/%s", address)
		log.Printf("DEBUG: Trying fallback location: %s", fallbackPath)
		file, err = os.OpenFile(fallbackPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
		if err != nil {
			log.Printf("ERROR: Also failed to use fallback location: %v", err)
			return err
		}
		log.Printf("DEBUG: Successfully opened fallback file")
	} else {
		log.Printf("DEBUG: Successfully opened file at primary location")
	}
	defer file.Close()

	// Write only the token to the file
	if _, err := file.WriteString(freshToken + "\n"); err != nil {
		log.Printf("Failed to write to file %s: %v", tokenPath, err)
		return err
	}

	return nil
}
