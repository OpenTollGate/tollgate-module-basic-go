# Handover: Fix "Outputs Already Signed" Cashu Swap Bug

**Status:** Ready for implementation
**Date:** 2026-07-17
**Repos affected:**
1. `~/net4sats/gonuts-tollgate/` — Cashu library fork (remote: `opentollgate` → `https://github.com/OpenTollGate/gonuts-tollgate.git`)
2. `~/repos/tollgate-module-basic-go/` — TollGate backend (remote: `github-https` → `https://github.com/OpenTollGate/tollgate-module-basic-go.git`)

---

## 1. Problem Description

When a user submits a Cashu token to the TollGate backend and the swap to the
trusted mint fails (e.g. network timeout, mint 500 error, lightning payment
failure in cross-mint swap), a retry with the same token produces:

> `blinded message already signed` (Cashu error code 10002)

The mint rejects the retry because the **same blinded messages** are sent
again. The wallet generates deterministic blinded messages from a keyset
counter, and that counter was never incremented because the previous swap
failed before the increment step.

**User-visible symptom:** First attempt fails with a generic error. Retrying
the same token always fails with "blinded message already signed" — the token
is effectively bricked from the user's perspective, even though the underlying
proofs have NOT been spent.

---

## 2. Root Cause: Keyset Counter Race Condition

### How deterministic secrets work

The wallet generates deterministic secrets for blinded messages using a
per-keyset counter stored in the DB. The counter is read, used to derive
secrets via `createBlindedMessages()`, and then incremented after a successful
swap.

### The buggy flow (in `wallet.go` `Receive()`)

1. `createSwapRequest()` (line 761) reads the keyset counter from DB
2. `createBlindedMessages()` (line 766) generates blinded messages using that counter value
3. `swap()` (line 660) sends the swap request to the mint
4. **If swap fails** → return error at line 662, counter is **NOT incremented**
5. On retry, `createSwapRequest()` reads the **same counter**, generates the **same blinded messages**
6. Mint sees duplicate blinded messages → returns `BlindedMessageAlreadySigned` (code 10002)

### Why the mint rejects

In `mint/mint.go` (lines 523-530), the mint checks `m.db.GetBlindSignatures(B_s)`
— if it has already signed any of the blinded messages, it returns
`cashu.BlindedMessageAlreadySigned` immediately. The mint persists blind
signatures even if the overall swap response was never consumed by the client.

### The critical code path

**`wallet/wallet.go` lines 660-670 (Receive, non-swapToTrusted branch):**
```go
// line 660
newProofs, err := swap(tokenMint, req)
if err != nil {
    return 0, fmt.Errorf("could not swap proofs: %v", err)  // line 662 — COUNTER NOT INCREMENTED
}

w.mu.Lock()
defer w.mu.Unlock()

if err = w.db.IncrementKeysetCounter(req.keyset.Id, uint32(len(req.outputs))); err != nil {  // line 668 — TOO LATE
    return 0, fmt.Errorf("error incrementing keyset counter: %v", err)
}
```

**Same pattern in `ReceiveHTLC` at lines 733-738:**
```go
newProofs, err := swap(tokenMint, req)
if err != nil {
    return 0, fmt.Errorf("could not swap proofs: %v", err)  // line 735
}

err = w.db.IncrementKeysetCounter(req.keyset.Id, uint32(len(req.outputs)))  // line 738 — TOO LATE
```

**Same pattern in `swapToSend` reclaim at lines 2132-2136:**
```go
newProofs, err := swap(mintURL, req)
if err != nil {
    return 0, fmt.Errorf("could not swap proofs: %v", err)  // line 2134
}
err = w.db.IncrementKeysetCounter(req.keyset.Id, uint32(len(req.outputs)))  // line 2136
```

### Additional issue: `PostSwap` does not return typed errors

`PostSwap` in `wallet/client/client.go` (lines 173-196) calls `httpPost()`
which calls `parse()` (line 344). `parse()` correctly decodes HTTP 400
responses into `cashu.Error` typed values. **However**, the error is returned
from `httpPost` → `PostSwap` → `swap()` → `Receive()` and by the time it
reaches `TollWallet.Receive()`, callers cannot reliably detect the
"already signed" error because:

1. The error gets wrapped in `fmt.Errorf("could not swap proofs: %v", err)` at multiple levels, losing the type
2. `TollWallet.Receive()` (tollwallet.go line 134) only checks `strings.Contains(err.Error(), "Token already spent")` — it does not detect "already signed"

This means even if the counter race is fixed, a stale "already signed" error
from a previous failed attempt would still surface to the user as a generic
"Payment processing failed" message.

---

## 3. Exact File Paths and Verified Line Numbers

All line numbers verified against the working tree as of 2026-07-17.

### Repo 1: `~/net4sats/gonuts-tollgate/`

| File | Lines | What |
|------|-------|------|
| `wallet/wallet.go` | 586-677 | `Receive()` — main entry point |
| `wallet/wallet.go` | 660-662 | swap call + error return (THE BUG — counter not incremented on failure) |
| `wallet/wallet.go` | 665-670 | lock + counter increment (too late, after swap success) |
| `wallet/wallet.go` | 700-749 | `ReceiveHTLC()` — same pattern |
| `wallet/wallet.go` | 733-738 | swap + counter increment in ReceiveHTLC (same bug) |
| `wallet/wallet.go` | 752-778 | `createSwapRequest()` — reads counter, generates blinded messages |
| `wallet/wallet.go` | 762 | `keysetCounter := w.counterForKeyset(mint.activeKeyset.Id)` |
| `wallet/wallet.go` | 766 | `createBlindedMessages(split, mint.activeKeyset.Id, &keysetCounter)` |
| `wallet/wallet.go` | 780-803 | `swap()` — calls `client.PostSwap()`, unblinds signatures |
| `wallet/wallet.go` | 807-836 | `swapToTrusted()` — cross-mint swap path |
| `wallet/wallet.go` | 1228-1281 | `swapProofs()` — melt+mint cross-mint swap |
| `wallet/wallet.go` | 1734-1776 | `createBlindedMessages()` — deterministic secret generation |
| `wallet/wallet.go` | 1757-1762 | counter increment inside loop: `*counter++` |
| `wallet/wallet.go` | 1927-1929 | `counterForKeyset()` — reads from DB |
| `wallet/wallet.go` | 2126-2148 | `swapToSend` reclaim path (same counter-after-swap pattern) |
| `cashu/cashu.go` | 471-485 | `Error` struct + `Error()` method |
| `cashu/cashu.go` | 496 | `BlindedMessageAlreadySignedErrCode = 10002` |
| `cashu/cashu.go` | 531 | `BlindedMessageAlreadySigned` sentinel error var |
| `mint/mint.go` | 523-530 | Mint checks for already-signed blinded messages |
| `wallet/client/client.go` | 173-196 | `PostSwap()` — does not check HTTP status or parse error JSON itself |
| `wallet/client/client.go` | 335-342 | `httpPost()` — calls `parse()` |
| `wallet/client/client.go` | 344-363 | `parse()` — handles HTTP 400 (returns `cashu.Error`), handles non-200 |

### Repo 2: `~/repos/tollgate-module-basic-go/`

| File | Lines | What |
|------|-------|------|
| `src/tollwallet/tollwallet.go` | 17 | `ErrTokenAlreadySpent` sentinel error |
| `src/tollwallet/tollwallet.go` | 112-141 | `TollWallet.Receive()` — wraps `wallet.Receive()` |
| `src/tollwallet/tollwallet.go` | 127-137 | error handling — only checks for "Token already spent" |
| `src/merchant/merchant.go` | 455 | `m.tollwallet.Receive(paymentCashuToken)` call |
| `src/merchant/merchant.go` | 475-497 | error classification — only handles `ErrTokenAlreadySpent` |
| `src/tollwallet/go.mod` | 13 | replace directive: `github.com/Origami74/gonuts-tollgate => github.com/OpenTollGate/gonuts-tollgate v0.7.3` |

---

## 4. Code Snippets: Current Buggy Code and Proposed Fix

### 4A. Fix: Move counter increment BEFORE swap (Optimistic Increment)

**File:** `~/net4sats/gonuts-tollgate/wallet/wallet.go`

#### Current (lines 647-676, Receive non-swapToTrusted branch):

```go
		req, err := w.createSwapRequest(proofsToSwap, &mint)
		if err != nil {
			return 0, fmt.Errorf("could not create swap request: %v", err)
		}

		//if P2PK locked ecash has `SIG_ALL` flag, sign outputs
		if nut10Secret.Kind == nut10.P2PK && nut11.IsSigAll(nut10Secret) {
			req.outputs, err = nut11.AddSignatureToOutputs(req.outputs, w.privateKey)
			if err != nil {
				return 0, fmt.Errorf("error signing outputs: %v", err)
			}
		}

		newProofs, err := swap(tokenMint, req)
		if err != nil {
			return 0, fmt.Errorf("could not swap proofs: %v", err)
		}

		w.mu.Lock()
		defer w.mu.Unlock()

		if err = w.db.IncrementKeysetCounter(req.keyset.Id, uint32(len(req.outputs))); err != nil {
			return 0, fmt.Errorf("error incrementing keyset counter: %v", err)
		}

		if err := w.db.SaveProofs(newProofs); err != nil {
			return 0, fmt.Errorf("error storing proofs: %v", err)
		}
		return newProofs.Amount(), nil
```

#### Proposed fix:

```go
		w.mu.Lock()
		defer w.mu.Unlock()

		// Optimistically increment the keyset counter BEFORE the swap.
		// This ensures that if the swap fails and the caller retries,
		// createSwapRequest will generate fresh blinded messages with
		// a new counter value, avoiding "blinded message already signed"
		// errors from the mint.
		if err = w.db.IncrementKeysetCounter(req.keyset.Id, uint32(len(req.outputs))); err != nil {
			return 0, fmt.Errorf("error incrementing keyset counter: %v", err)
		}

		newProofs, err := swap(tokenMint, req)
		if err != nil {
			return 0, fmt.Errorf("could not swap proofs: %v", err)
		}

		if err := w.db.SaveProofs(newProofs); err != nil {
			return 0, fmt.Errorf("error storing proofs: %v", err)
		}
		return newProofs.Amount(), nil
```

**Key change:** The `w.mu.Lock()` + `IncrementKeysetCounter()` now happens
BEFORE `swap()`. The `createSwapRequest()` call already read the counter and
generated blinded messages using the pre-increment value. By incrementing
before the network call, the DB counter advances regardless of swap outcome.
On retry, `createSwapRequest()` will read the incremented counter and generate
different blinded messages.

**Note on `createSwapRequest`:** This function (line 761) reads the counter
with `w.counterForKeyset(mint.activeKeyset.Id)` and passes `&keysetCounter` to
`createBlindedMessages()`. Inside `createBlindedMessages()` (line 1762), the
local copy is incremented with `*counter++`. This local increment does NOT
write to the DB — only `w.db.IncrementKeysetCounter()` does. So moving the DB
increment before `swap()` is safe: the blinded messages already generated use
the old counter, and the DB is now ready for the next call.

#### Same fix for `ReceiveHTLC` (lines 720-748):

**Current:**
```go
		req, err := w.createSwapRequest(proofs, &mint)
		// ... (SIG_ALL signing) ...

		newProofs, err := swap(tokenMint, req)
		if err != nil {
			return 0, fmt.Errorf("could not swap proofs: %v", err)
		}

		err = w.db.IncrementKeysetCounter(req.keyset.Id, uint32(len(req.outputs)))
		if err != nil {
			return 0, fmt.Errorf("error incrementing keyset counter: %v", err)
		}
```

**Proposed:**
```go
		// Optimistically increment counter before swap (see Receive for rationale)
		err = w.db.IncrementKeysetCounter(req.keyset.Id, uint32(len(req.outputs)))
		if err != nil {
			return 0, fmt.Errorf("error incrementing keyset counter: %v", err)
		}

		newProofs, err := swap(tokenMint, req)
		if err != nil {
			return 0, fmt.Errorf("could not swap proofs: %v", err)
		}
```

#### Same fix for reclaim path (lines 2128-2142):

**Current:**
```go
			newProofs, err := swap(mintURL, req)
			if err != nil {
				return 0, fmt.Errorf("could not swap proofs: %v", err)
			}
			err = w.db.IncrementKeysetCounter(req.keyset.Id, uint32(len(req.outputs)))
```

**Proposed:**
```go
			err = w.db.IncrementKeysetCounter(req.keyset.Id, uint32(len(req.outputs)))
			if err != nil {
				return 0, fmt.Errorf("error incrementing keyset counter: %v", err)
			}
			newProofs, err := swap(mintURL, req)
			if err != nil {
				return 0, fmt.Errorf("could not swap proofs: %v", err)
			}
```

### 4B. Fix: Add retry on "already signed" error with fresh blinded messages

**File:** `~/net4sats/gonuts-tollgate/wallet/wallet.go`

Even with the optimistic increment, there may be edge cases where a stale
"already signed" error is received (e.g. from a crash between swap success and
counter increment in an older version). Add a retry wrapper around the `swap()`
call in `Receive()`:

#### Proposed retry logic in `Receive()` (replace the swap call section):

```go
		newProofs, err := w.swapWithRetry(tokenMint, req, proofsToSwap, &mint, nut10Secret)
		if err != nil {
			return 0, fmt.Errorf("could not swap proofs: %v", err)
		}
```

#### New helper function (add near `swap()` around line 803):

```go
// swapWithRetry calls swap() and retries once with fresh blinded messages
// if the mint returns a "blinded message already signed" error.
func (w *Wallet) swapWithRetry(
	mintURL string,
	req swapRequestPayload,
	proofs cashu.Proofs,
	mint *walletMint,
	nut10Secret nut10.Secret,
) (cashu.Proofs, error) {
	newProofs, err := swap(mintURL, req)
	if err != nil {
		// Check if the error is a Cashu "already signed" error
		var cashuErr cashu.Error
		if errors.As(err, &cashuErr) && cashuErr.Code == cashu.BlindedMessageAlreadySignedErrCode {
			// Retry with fresh blinded messages using the next counter values
			req, err = w.createSwapRequest(proofs, mint)
			if err != nil {
				return nil, fmt.Errorf("could not create retry swap request: %v", err)
			}
			// Re-apply SIG_ALL signing if needed
			if nut10Secret.Kind == nut10.P2PK && nut11.IsSigAll(nut10Secret) {
				req.outputs, err = nut11.AddSignatureToOutputs(req.outputs, w.privateKey)
				if err != nil {
					return nil, fmt.Errorf("error signing outputs on retry: %v", err)
				}
			}
			return swap(mintURL, req)
		}
		return nil, err
	}
	return newProofs, nil
}
```

**Note:** The `errors.As` check works because `parse()` in `client.go` (line 346)
decodes HTTP 400 responses into `cashu.Error` (a struct with `Error()` method,
satisfying the `error` interface). The error type flows through `httpPost` →
`PostSwap` → `swap()` without being wrapped (line 181: `return nil, err`).

**Important:** You need to add `"errors"` to the import block in `wallet.go` if
not already present. Check the existing imports — `errors` is already imported
(used for `errors.New` at line 604).

### 4C. Fix: `PostSwap` error handling (already mostly correct, verify only)

**File:** `~/net4sats/gonuts-tollgate/wallet/client/client.go`

**Current state (lines 173-196):** `PostSwap` calls `httpPost()` which calls
`parse()`. `parse()` (line 344-352) already decodes HTTP 400 into `cashu.Error`
and returns it as a typed error. `PostSwap` passes this through at line 181
(`return nil, err`).

**Assessment:** The typed error DOES flow through. The problem is that callers
wrap it with `fmt.Errorf("could not swap proofs: %v", err)` which uses `%v`
not `%w`, breaking `errors.As`/`errors.Is` chains.

**Fix required:** Change error wrapping in `swap()` and `Receive()` to use
`%w` instead of `%v` so the typed error is preserved:

**In `swap()` (line 786-787):**
```go
// Current:
return nil, err
// This is fine — swap() passes through the raw error from PostSwap
```

**In `Receive()` (line 662):**
```go
// Current:
return 0, fmt.Errorf("could not swap proofs: %v", err)
// Change to:
return 0, fmt.Errorf("could not swap proofs: %w", err)
```

Apply the same `%v` → `%w` change to all swap error wrapping sites:
- Line 662: `Receive` non-swapToTrusted
- Line 633: `Receive` swapToTrusted  
- Line 735: `ReceiveHTLC`
- Line 824: `swapToTrusted` internal swap
- Line 2134: reclaim path

### 4D. Fix: Add "already signed" detection in TollWallet

**File:** `~/repos/tollgate-module-basic-go/src/tollwallet/tollwallet.go`

**Current (lines 127-137):**
```go
	amountAfterSwap, err := w.wallet.Receive(token, swapToTrusted)
	if err != nil {
		if strings.Contains(err.Error(), "Token already spent") {
			return 0, fmt.Errorf("%w: %v", ErrTokenAlreadySpent, err)
		}
		return 0, err
	}
```

**Proposed:**
```go
	amountAfterSwap, err := w.wallet.Receive(token, swapToTrusted)
	if err != nil {
		if strings.Contains(err.Error(), "Token already spent") {
			return 0, fmt.Errorf("%w: %v", ErrTokenAlreadySpent, err)
		}
		// Detect "blinded message already signed" — this means a prior
		// swap attempt used the same blinded messages. The optimistic
		// counter increment in the library should prevent this, but we
		// handle it gracefully with a user-friendly message.
		if strings.Contains(err.Error(), "blinded message already signed") {
			return 0, fmt.Errorf("%w: %v", ErrOutputsAlreadySigned, err)
		}
		return 0, err
	}
```

**Add new sentinel error (near line 17):**
```go
var ErrTokenAlreadySpent = errors.New("Token already spent")
var ErrOutputsAlreadySigned = errors.New("Outputs already signed")
```

### 4E. Fix: Add user-friendly error in merchant

**File:** `~/repos/tollgate-module-basic-go/src/merchant/merchant.go`

**Current (lines 478-490):**
```go
	if !errors.Is(err, tollwallet.ErrTokenAlreadySpent) {
		m.mintHealthTracker.MarkUnreachable(mintURL)
	}

	var errorCode string
	var errorMessage string

	if errors.Is(err, tollwallet.ErrTokenAlreadySpent) {
		errorCode = "payment-error-token-spent"
		errorMessage = "Token has already been spent"
	} else {
		errorCode = "payment-processing-failed"
		errorMessage = fmt.Sprintf("Payment processing failed: %v", err)
	}
```

**Proposed:**
```go
	if !errors.Is(err, tollwallet.ErrTokenAlreadySpent) && !errors.Is(err, tollwallet.ErrOutputsAlreadySigned) {
		m.mintHealthTracker.MarkUnreachable(mintURL)
	}

	var errorCode string
	var errorMessage string

	if errors.Is(err, tollwallet.ErrTokenAlreadySpent) {
		errorCode = "payment-error-token-spent"
		errorMessage = "Token has already been spent"
	} else if errors.Is(err, tollwallet.ErrOutputsAlreadySigned) {
		errorCode = "payment-error-outputs-signed"
		errorMessage = "This token was already partially processed. Please try again with a new token."
	} else {
		errorCode = "payment-processing-failed"
		errorMessage = fmt.Sprintf("Payment processing failed: %v", err)
	}
```

### 4F. Update go.mod replace directive

**File:** `~/repos/tollgate-module-basic-go/src/tollwallet/go.mod`

**Current (line 13):**
```
github.com/Origami74/gonuts-tollgate => github.com/OpenTollGate/gonuts-tollgate v0.7.3
```

After publishing the gonuts-tollgate fix (tagged as e.g. `v0.7.4`), update to:
```
github.com/Origami74/gonuts-tollgate => github.com/OpenTollGate/gonuts-tollgate v0.7.4
```

Then run `go mod tidy` from `src/`.

---

## 5. Step-by-Step Implementation Guide

### Step 1: Fix gonuts-tollgate (the library)

All work in `~/net4sats/gonuts-tollgate/`.

1. **Create a branch:**
   ```bash
   cd ~/net4sats/gonuts-tollgate
   git checkout main
   git pull
   git checkout -b fix/swap-counter-race
   ```

2. **Edit `wallet/wallet.go` — Receive() non-swapToTrusted branch (around lines 647-675):**
   - Move the `w.mu.Lock()` / `defer w.mu.Unlock()` / `w.db.IncrementKeysetCounter()` block to BEFORE the `swap()` call
   - Change `fmt.Errorf("could not swap proofs: %v", err)` to `%w` at line 662
   - Change `fmt.Errorf("error swapping token to trusted mint: %v", err)` to `%w` at line 633

3. **Edit `wallet/wallet.go` — ReceiveHTLC() (around lines 720-748):**
   - Move `w.db.IncrementKeysetCounter()` to before `swap()` call
   - Change `%v` to `%w` in the swap error wrapping at line 735

4. **Edit `wallet/wallet.go` — swapToTrusted() internal swap (line 824):**
   - Change `%v` to `%w` in the error wrapping

5. **Edit `wallet/wallet.go` — reclaim path (around lines 2128-2136):**
   - Move `IncrementKeysetCounter` before `swap()` call
   - Change `%v` to `%w` at line 2134

6. **Add `swapWithRetry()` helper function** in `wallet/wallet.go` (after the `swap()` function, around line 803):
   - See code snippet in section 4B above
   - Requires importing `nut10` if not already imported (check — it IS already imported, used at line 608)

7. **Update `Receive()` to use `swapWithRetry()` instead of direct `swap()` call:**
   - Replace the `swap(tokenMint, req)` call at line 660 with `w.swapWithRetry(tokenMint, req, proofsToSwap, &mint, nut10Secret)`
   - Do the same in `ReceiveHTLC()` at line 733

8. **Verify the build:**
   ```bash
   cd ~/net4sats/gonuts-tollgate
   gofmt -l .
   go vet ./...
   go build ./...
   go test -race -count=1 ./...
   ```

9. **Commit and push:**
   ```bash
   git add -A
   git commit -m "fix: increment keyset counter before swap to prevent already-signed errors

   Move the keyset counter increment to before the swap() network call
   in Receive(), ReceiveHTLC(), and the reclaim path. This ensures
   that if a swap fails and is retried, fresh blinded messages are
   generated with a new counter value, avoiding 'blinded message
   already signed' errors from the mint.

   Also add swapWithRetry() as a safety net for edge cases where a
   stale already-signed error is received, and change error wrapping
   from %v to %w to preserve typed errors for callers."

   git push opentollgate fix/swap-counter-race
   ```

10. **Create PR on GitHub** for `OpenTollGate/gonuts-tollgate` from `fix/swap-counter-race` → `main`.

11. **After PR is merged, tag a new release:**
    ```bash
    git checkout main
    git pull
    git tag v0.7.4
    git push opentollgate v0.7.4
    ```
    *(Use the next appropriate version number — check existing tags with `git tag --sort=-v:refname | head`)*

### Step 2: Fix tollgate-module-basic-go (the backend)

All work in `~/repos/tollgate-module-basic-go/`.

1. **Create a branch:**
   ```bash
   cd ~/repos/tollgate-module-basic-go
   git checkout main
   git pull
   git checkout -b fix/swap-already-signed
   ```

2. **Edit `src/tollwallet/tollwallet.go`:**
   - Add `ErrOutputsAlreadySigned` sentinel error (after line 17)
   - Add detection for "blinded message already signed" in `Receive()` (after line 136)

3. **Edit `src/merchant/merchant.go`:**
   - Add `ErrOutputsAlreadySigned` branch in the error classification (around lines 478-490)

4. **Edit `src/tollwallet/go.mod`:**
   - Update the replace directive version to the new gonuts-tollgate tag (e.g. `v0.7.4`)
   - Run `go mod tidy` from `src/`

5. **Verify the build:**
   ```bash
   cd ~/repos/tollgate-module-basic-go/src
   gofmt -l .
   go vet ./...
   go build ./...
   go test -race -count=1 -tags testenv ./...
   ```

6. **Add CHANGELOG entry** in `CHANGELOG.md` under `[Unreleased]` → `Fixed`:
   ```markdown
   - **Fixed** "outputs already signed" error on retry by incrementing keyset
     counter before swap call and adding automatic retry with fresh blinded
     messages ([#N](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/N))
   ```

7. **Commit and push:**
   ```bash
   git add -A
   git commit -m "fix: handle 'outputs already signed' Cashu swap error

   Add ErrOutputsAlreadySigned sentinel error and user-friendly error
   message when a swap retry encounters already-signed blinded messages.
   Update gonuts-tollgate dependency to v0.7.4 which fixes the keyset
   counter race condition."

   git push fork fix/swap-already-signed
   ```

8. **Create PR** on GitHub for `OpenTollGate/tollgate-module-basic-go` from `c03rad0r/test-stablechannel-tollgate-module-basic-go:fix/swap-already-signed` → `main`.

---

## 6. Testing Instructions

### Unit Tests

**In gonuts-tollgate:**

1. Write a test that simulates a failed swap followed by a retry:
   - Mock or stub `client.PostSwap` to fail on first call, succeed on second
   - Call `Wallet.Receive()` with a test token
   - Verify the first call fails but the counter has been incremented
   - Call `Receive()` again and verify it succeeds with fresh blinded messages

2. Test `swapWithRetry()` directly:
   - First `swap()` returns `cashu.Error{Code: BlindedMessageAlreadySignedErrCode}`
   - Verify retry is attempted with new `swapRequestPayload`
   - Verify second `swap()` succeeds

3. Test that `errors.As` can unwrap the "already signed" error through the
   `fmt.Errorf("...: %w", err)` wrapping

**In tollgate-module-basic-go:**

4. Test `TollWallet.Receive()` returns `ErrOutputsAlreadySigned` when the
   underlying error contains "blinded message already signed"

5. Test `merchant.PurchaseSession()` returns the correct error code
   (`payment-error-outputs-signed`) and user-friendly message

### Integration Test (against a live mint)

```bash
# From tollgate-module-basic-go/src
go test -race -count=1 -tags testenv ./src/tollwallet/ -run TestReceiveRetry
```

### Manual Test (on a real router or dev environment)

1. Generate a Cashu token
2. Submit it to the TollGate backend
3. Kill the mint process mid-swap (simulating failure)
4. Restart the mint
5. Resubmit the same token
6. **Expected:** Token is accepted (not "already signed")
7. **Without fix:** Token fails with "blinded message already signed"

---

## 7. Git Workflow

### gonuts-tollgate

```
Branch: fix/swap-counter-race
Base: main
Remote: opentollgate (https://github.com/OpenTollGate/gonuts-tollgate.git)

Commit message:
  fix: increment keyset counter before swap to prevent already-signed errors

  Move the keyset counter increment to before the swap() network call
  in Receive(), ReceiveHTLC(), and the reclaim path. Also add
  swapWithRetry() safety net and change %v to %w for error wrapping.

PR target: OpenTollGate/gonuts-tollgate main
Tag after merge: v0.7.4 (or next appropriate semver)
```

### tollgate-module-basic-go

```
Branch: fix/swap-already-signed
Base: main
Remote: fork (https://github.com/c03rad0r/test-stablechannel-tollgate-module-basic-go.git)
PR target: OpenTollGate/tollgate-module-basic-go main

Commit message:
  fix: handle 'outputs already signed' Cashu swap error

  Add ErrOutputsAlreadySigned sentinel and user-friendly error message.
  Update gonuts-tollgate dependency to v0.7.4.

PR title: fix: resolve "outputs already signed" error on Cashu swap retry
```

### Pre-PR Checklist (from AGENTS.md)

Before opening either PR, run from the respective `src/` or repo root:

```bash
gofmt -l .          # must print nothing
go vet ./...
go build ./...
go test -race -count=1 -tags testenv ./...  # tollgate-module-basic-go only
```

For tollgate-module-basic-go, also add a CHANGELOG.md entry under `[Unreleased]`.

No coding-assistant attribution in commits or PR bodies.

---

## Appendix: All IncrementKeysetCounter Call Sites

For completeness, here are ALL 7 sites where `IncrementKeysetCounter` is called
in `wallet/wallet.go`. The three marked with **BUG** have the counter-after-swap
pattern. The others increment after operations that don't involve the same
retry risk (mint after melt, change from melt, etc.) but should be reviewed:

| Line | Function | Pattern | Needs Fix? |
|------|----------|---------|------------|
| 415 | `MintTokens` | After mint success | No — mint quotes are one-shot |
| 668 | `Receive` (non-swap) | After swap success | **YES — BUG** |
| 738 | `ReceiveHTLC` | After swap success | **YES — BUG** |
| 906 | melt quote state check | After melt change | No — change from completed melt |
| 1067 | `MeltBolt11` change | After melt change | No — change from completed melt |
| 1533 | `Send` swap | After swap success | Review — may need fix |
| 2136 | `swapToSend` reclaim | After swap success | **YES — BUG** |

**Line 1533** (`Send` path) should also be reviewed: if a swap in `Send` fails
and is retried, the same issue could occur. However, `Send` is typically called
by the wallet itself (not user-triggered with the same inputs), so the risk is
lower. Still worth fixing for consistency.