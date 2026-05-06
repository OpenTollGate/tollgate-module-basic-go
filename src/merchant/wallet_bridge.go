package merchant

import (
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

func newTollWallet(walletPath string, mintURLs []string) (Wallet, error) {
	tw, err := tollwallet.New(walletPath, mintURLs, false)
	if err != nil {
		return nil, err
	}
	return tw, nil
}
