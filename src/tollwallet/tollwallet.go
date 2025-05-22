package tollwallet

import (
	"fmt"
	"os"

	"github.com/elnosh/gonuts/cashu"
	"github.com/elnosh/gonuts/wallet"
)

// TollWallet represents a Cashu wallet that can receive, swap, and send tokens
type TollWallet struct {
	wallet                     *wallet.Wallet
	acceptedMints              []string
	allowAndSwapUntrustedMints bool
}

// New creates a new Cashu wallet instance
func New(walletPath string, acceptedMints []string, allowAndSwapUntrustedMints bool) (*TollWallet, error) {

	// TODO: We want to restore from our mnemnonic seed phrase on startup as we have to keep our db in memory
	// TODO: Copy approach from alby: https://github.com/getAlby/hub/blob/158d4a2539307bda289149792c3748d44c9fed37/lnclient/cashu/cashu.go#L46

	if len(acceptedMints) < 1 {
		fmt.Errorf("FATAL: Wallet requires at least 1 accepted mint, none were provided")
		os.Exit(1)
	}

	config := wallet.Config{WalletPath: walletPath, CurrentMintURL: acceptedMints[0]}
	cashuWallet, err := wallet.LoadWallet(config)

	if err != nil {
		return nil, fmt.Errorf("failed to create wallet: %w", err)
	}

	return &TollWallet{
		wallet:                     cashuWallet,
		acceptedMints:              acceptedMints,
		allowAndSwapUntrustedMints: allowAndSwapUntrustedMints,
	}, nil
}

func (w *TollWallet) Receive(token cashu.Token) error {
	mint := token.Mint()

	swapToTrusted := false

	// If mint is untrusted, check if operator allows swapping or rejects untrusted mints.
	if !contains(w.acceptedMints, mint) {
		if !w.allowAndSwapUntrustedMints {
			return fmt.Errorf("Token rejected. Token for mint %s is not accepted and wallet does not allow swapping of untrusted mints.", mint)
		}
		swapToTrusted = true
	}

	_, err := w.wallet.Receive(token, swapToTrusted)
	return err
}

func (w *TollWallet) Send(amount uint64, mintUrl string, includeFees bool) (cashu.Token, error) {
	proofs, err := w.wallet.Send(amount, mintUrl, includeFees)

	if err != nil {
		return nil, fmt.Errorf("Failed to send %d to %s: %w", amount, mintUrl, err)
	}

	token, err := cashu.NewTokenV4(proofs, mintUrl, cashu.Sat, true) // TODO: Support multi unit

	return token, nil
}

func (w *TollWallet) ParseToken(token string) (cashu.Token, error) {
	return cashu.DecodeToken(token)
}

// contains checks if a string exists in a slice of strings
func contains(slice []string, str string) bool {
	for _, item := range slice {
		if item == str {
			return true
		}
	}
	return false
}

// GetBalance returns the current balance of the wallet
func (w *TollWallet) GetBalance() uint64 {
	balance := w.wallet.GetBalance()

	return balance
}
