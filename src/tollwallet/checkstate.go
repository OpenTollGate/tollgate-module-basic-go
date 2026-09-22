package tollwallet

import (
	"encoding/hex"
	"fmt"

	"github.com/OpenTollGate/gonuts-tollgate/cashu"
	"github.com/OpenTollGate/gonuts-tollgate/cashu/nuts/nut07"
	gonutscrypto "github.com/OpenTollGate/gonuts-tollgate/crypto"
	"github.com/OpenTollGate/gonuts-tollgate/wallet/client"
)

// Token liveness checking (NUT-07). This is the reconciliation primitive
// drain recovery is built on: the drain journal records tokens that were
// irreversibly produced, and only the mint can say whether a journaled
// token is still spendable. A PENDING proof is deliberately reported as
// not spendable: redemption is in flight, so treating the token as
// recoverable funds would be wrong.
//
// NUT #07: When `Alice` prepares a token to be sent to `Carol`, she can mark these tokens in her database as _pending_. She can then, periodically or upon user input, check with the mint if the token is `UNSPENT` or whether it has been redeemed by `Carol` already, i.e., is `SPENT`. If the proof is not spendable anymore (and, thus, has been redeemed by `Carol`), she can safely delete the proof from her database.
// NUT #07: - A proof is `UNSPENT` if it has not been spent yet
// NUT #07: - A proof is `PENDING` if it is being processed in a transaction (in an ongoing payment). A `PENDING` proof cannot be used in another transaction until it is `live` again.
// NUT #07: - A proof is `SPENT` if it has been redeemed and its secret is in the list of spent secrets of the mint.
func CheckTokenSpendable(tokenStr string) (bool, error) {
	token, err := cashu.DecodeToken(tokenStr)
	if err != nil {
		return false, fmt.Errorf("decode token: %w", err)
	}

	ys := make([]string, 0, len(token.Proofs()))
	for _, proof := range token.Proofs() {
		y, err := gonutscrypto.HashToCurve([]byte(proof.Secret))
		if err != nil {
			return false, fmt.Errorf("derive proof Y: %w", err)
		}
		ys = append(ys, hex.EncodeToString(y.SerializeCompressed()))
	}

	response, err := client.PostCheckProofState(token.Mint(), nut07.PostCheckStateRequest{Ys: ys})
	if err != nil {
		return false, fmt.Errorf("check proof state at mint %s: %w", token.Mint(), err)
	}

	spendable := true
	for _, proofState := range response.States {
		if proofState.State != nut07.Unspent {
			spendable = false
		}
	}
	return spendable, nil
}
