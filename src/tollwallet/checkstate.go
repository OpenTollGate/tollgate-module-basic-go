package tollwallet

import (
	"encoding/hex"
	"fmt"
	"time"

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

// checkStateBudget bounds one mint checkstate round-trip. The gonuts
// client takes no context, so a wedged mint (accepts the connection,
// never answers — #525's class) would otherwise park the caller on the
// client's full retry ladder. On expiry the check errors — which callers
// must treat as unknown, never spent — and the abandoned request
// goroutine terminates on the client's own schedule.
var checkStateBudget = 10 * time.Second

func postCheckProofStateBounded(mintURL string, request nut07.PostCheckStateRequest) (*nut07.PostCheckStateResponse, error) {
	type checkResult struct {
		response *nut07.PostCheckStateResponse
		err      error
	}
	done := make(chan checkResult, 1)
	go func() {
		response, err := client.PostCheckProofState(mintURL, request)
		done <- checkResult{response: response, err: err}
	}()

	select {
	case result := <-done:
		return result.response, result.err
	case <-time.After(checkStateBudget):
		return nil, fmt.Errorf("mint %s did not answer checkstate within %s", mintURL, checkStateBudget)
	}
}

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

	response, err := postCheckProofStateBounded(token.Mint(), nut07.PostCheckStateRequest{Ys: ys})
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
