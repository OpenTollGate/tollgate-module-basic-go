// Command demo runs the bearer-instrument port end to end, one instrument kind
// at a time, and narrates the two derivations the port is built on:
//
//	NUT-07  — "is this instrument spent?"  => the authority's spend memory
//	NUT-09  — "recover my funds from seed" => the authority's issuance memory
//
// It is deliberately self-narrating, so the transcript IS the artifact: every
// value below is produced by real execution, and `go test ./...` asserts the
// same rules.
package main

import (
	"errors"
	"fmt"
	"strings"

	bearerport "bearerport"
)

func main() {
	cashu := bearerport.NewCashuAuthority("https://mint.example")
	fedi := bearerport.NewFedimintAuthority("fed1invitefedimintexample")
	pow := bearerport.NewPowAuthority("miner-pubkey-01")

	port := bearerport.NewPort()
	port.Register(cashu)
	port.Register(fedi)
	port.Register(pow)
	seed := port.Seed()

	h("A bearer instrument is anything an authority can both recognise and remember")
	fmt.Println("The port is generic because it never parses an instrument's internals.")
	fmt.Println("It asks its authority exactly two questions, and those are NUT-07 and NUT-09.")

	h("1. The declared capability matrix")
	fmt.Printf("  %-34s %-8s %-6s %-8s %s\n", "AUTHORITY", "KIND", "UNIT", "STATE", "RECOVERY")
	for _, c := range port.Capabilities() {
		fmt.Printf("  %-34s %-8s %-6s %-8s %s\n", c.Authority, c.Kind, c.Unit, c.StateCheck, c.Recover)
	}
	fmt.Println()
	fmt.Println("  state=query -> the authority has a read-only liveness API.")
	fmt.Println("  state=probe -> it has NONE, so the state is learned by attempting the")
	fmt.Println("                 consumption. That is a mutation, and it has to be declared.")
	fmt.Println("  state=none  -> it cannot answer at all. The port reports UNKNOWN and says so.")
	fmt.Println("  recovery    -> WHERE the memory that makes NUT-09 possible actually lives.")

	h("2. One derivation, three encodings")
	fmt.Println("  The nullifier is HMAC(seed, counter) under a per-authority label:")
	fmt.Printf("    cashu mint   index 0 -> %s\n", cashu.Nullifier(seed, 0))
	fmt.Printf("    fedimint fed index 0 -> %s\n", fedi.Nullifier(seed, 0))
	fmt.Printf("    miner pool   index 0 -> (none: the ledger keys on the account, not the nonce)\n")
	fmt.Println()
	fmt.Println("  Same function, three instruments. It is scoped per authority on purpose:")
	fmt.Println("  a mint and a federation must not share a nullifier space, or accepting one")
	fmt.Println("  could make the port refuse the other. And spentness is NOT derived — it is")
	fmt.Println("  learned at redemption and remembered by the authority. That is half of NUT-07.")

	h("3. Face value is a claim; the authority's number is the fact")
	raw := cashu.Issue([]byte("a-foreign-holders-seed"), 0, 100)
	honest, err := port.Decode(raw)
	check(err)
	forged := []byte(strings.Replace(string(raw), ":100", ":1000", 1))
	lying, err := port.Decode(forged)
	check(err)
	fmt.Printf("  as issued  face value = %d\n", honest.FaceValue())
	fmt.Printf("  edited     face value = %d\n", lying.FaceValue())
	credited, err := port.Acquire(lying)
	check(err)
	fmt.Printf("  credited              = %d   <- the mint's record wins; the claim is ignored\n", credited)

	h("4. NUT-07, query mode: UNSPENT / SPENT / UNKNOWN")
	raw2 := cashu.Issue([]byte("holder-two"), 0, 50)
	in, err := port.Decode(raw2)
	check(err)
	fmt.Printf("  the mint signed it and Y is not spent -> %s\n", stateOf(port, in))

	never := bearerport.NullifierOf([]byte("a-different-holder"), 999, cashu.Name())
	states, err := cashu.CheckStates([]string{never})
	check(err)
	fmt.Printf("  a Y the mint never learned            -> %s   <- never UNSPENT\n", states[never])

	cashu.SetOffline(true)
	s, err := port.CheckState(in)
	fmt.Printf("  mint unreachable                      -> %v (%v)\n", s, err)
	cashu.SetOffline(false)

	if _, err := port.Acquire(in); err != nil {
		fmt.Printf("  consume it                            -> %v\n", err)
	}
	fmt.Printf("  after being consumed                  -> %s\n", stateOf(port, in))
	fmt.Println()
	fmt.Println("  The rule: SPENT and UNSPENT are things the authority SAID. Silence is UNKNOWN,")
	fmt.Println("  and UNKNOWN must fail closed. Mapping 'no answer' to 'unspent' is how a spent")
	fmt.Println("  note gets re-sold.")

	h("5. The sharp edge: a NUT-07 answer is about CONSUMPTION, never EXISTENCE")
	rawMint := bearerport.NewRawCashuAuthority("https://raw-mint.example")
	rawPort := bearerport.NewPort()
	rawPort.Register(rawMint)
	ghost := bearerport.NullifierOf([]byte("a-forged-note"), 0, rawMint.Name())
	gstates, err := rawMint.CheckStates([]string{ghost})
	check(err)
	fmt.Printf("  plain mint asked about a Y it never saw -> %s\n", gstates[ghost])
	_, err = rawPort.Decode([]byte("cashuA|deadbeef:5"))
	fmt.Printf("  ...but is that output one it ever signed? -> %v\n", err)
	fmt.Println()
	fmt.Println("  Both are true at once, and that is the point. A plain mint's /checkstate")
	fmt.Println("  reports membership in its SPENT set, so it says UNSPENT for values it has")
	fmt.Println("  never heard of. Existence is a different question, answered from the")
	fmt.Println("  issuance log — which is what Decode does. Conflating the two is exactly how")
	fmt.Println("  a forged note passes a naive 'is it spendable?' check.")

	h("6. NUT-07, probe mode: no liveness API, so the check IS a spend")
	fraw := fedi.Issue([]byte("a-foreign-holders-seed"), 0, 120)
	fin, err := port.Decode(fraw)
	check(err)
	before := port.BalanceByAuthority(fedi.Name())
	s, err = port.CheckState(fin)
	check(err)
	fmt.Printf("  probe a live note  -> %v   balance %d -> %d\n", s, before, port.BalanceByAuthority(fedi.Name()))
	s, err = port.CheckState(fin)
	check(err)
	fmt.Printf("  probe it again     -> %v   balance stays %d\n", s, port.BalanceByAuthority(fedi.Name()))
	fmt.Println()
	fmt.Println("  Asking the question consumed the note and credited the port. That is the")
	fmt.Println("  honest cost of a federation with no token-state endpoint, and it is why the")
	fmt.Println("  capability matrix is load-bearing rather than documentation.")

	h("7. NUT-07, no mode at all")
	praw, err := port.IssueInto(pow.Name(), 300)
	check(err)
	pin, err := port.Decode(praw)
	check(err)
	s, err = port.CheckState(pin)
	fmt.Printf("  miner pool asked about spentness -> %v (%v)\n", s, err)
	fmt.Println()
	fmt.Println("  The pool's ledger keys on the account, not the nonce, so it can restore the")
	fmt.Println("  balance and cannot answer spentness. UNKNOWN-with-a-reason is the correct")
	fmt.Println("  answer; inventing one would be a lie the port refuses to tell.")

	h("8. NUT-09, issuance-log mode: recovery replays the derivation")
	freshCashu := bearerport.NewCashuAuthority("https://mint.example")
	fresh := bearerport.NewPort()
	fresh.Register(freshCashu)
	for i := 0; i < 3; i++ {
		if _, err := fresh.IssueInto(freshCashu.Name(), 100); err != nil {
			check(err)
		}
	}
	fmt.Printf("  three outputs acquired      -> balance %d\n", fresh.BalanceByAuthority(freshCashu.Name()))
	freshCashu.MarkSpentForTest(bearerport.CommitmentOf(seed, 1, freshCashu.Name()))
	fmt.Println("  index 1 spent out of band   -> the mint remembers it as consumed")
	fresh.DestroyStore()
	fmt.Printf("  router flash lost           -> balance %d\n", fresh.Balance())
	rec, err := fresh.Recover(seed)
	check(err)
	fmt.Printf("  recover(seed)               -> %d   (2 of 3: the spent output must NOT return)\n",
		rec[freshCashu.Name()])
	fmt.Println()
	fmt.Println("  NUT-09 is this replay. The mint's signing log is the memory and the seed is")
	fmt.Println("  the index into it. Lose both and the funds are gone, which is why the counter")
	fmt.Println("  is persisted BEFORE an output is exposed, never after.")

	h("9. NUT-09 for the other instruments: different memory, same interface")
	mCashu := bearerport.NewCashuAuthority("https://mint.example")
	mFedi := bearerport.NewFedimintAuthority("fed1invitefedimintexample")
	mPow := bearerport.NewPowAuthority("miner-pubkey-01")
	multi := bearerport.NewPort()
	multi.Register(mCashu)
	multi.Register(mFedi)
	multi.Register(mPow)
	for _, tc := range []struct {
		a      bearerport.Authority
		amount uint64
	}{{mCashu, 100}, {mFedi, 200}, {mPow, 300}} {
		if _, err := multi.IssueInto(tc.a.Name(), tc.amount); err != nil {
			check(err)
		}
	}
	fmt.Printf("  before loss       -> cashu %d, fedi %d, pow %d\n",
		multi.BalanceByAuthority(mCashu.Name()),
		multi.BalanceByAuthority(mFedi.Name()),
		multi.BalanceByAuthority(mPow.Name()))
	multi.DestroyStore()
	rec, err = multi.Recover(multi.Seed())
	check(err)
	fmt.Printf("  after recover     -> cashu %d, fedi %d, pow %d\n",
		rec[mCashu.Name()], rec[mFedi.Name()], rec[mPow.Name()])
	fmt.Println()
	fmt.Println("  Cashu replays a signing log, fedimint restores the federation's backup of the")
	fmt.Println("  note set, a miner's pool hands back the payout ledger. Three different memories,")
	fmt.Println("  one interface — and the port never learns which is which.")

	h("10. Recovery is scoped to the holder")
	wrong, err := multi.Recover([]byte("someone-elses-seed"))
	check(err)
	fmt.Printf("  recover(a different seed) -> cashu %d\n", wrong[mCashu.Name()])
	fmt.Println()
	fmt.Println("  Recovery asked about commitments derived from OUR seed, and only outputs the")
	fmt.Println("  authority signed for those commitments answered. The seed itself never crossed")
	fmt.Println("  the wire, so a wrong seed is simply not in the log.")

	h("11. One spend path, three backends")
	sCashu := bearerport.NewCashuAuthority("https://mint.example")
	sFedi := bearerport.NewFedimintAuthority("fed1invitefedimintexample")
	sPow := bearerport.NewPowAuthority("miner-pubkey-01")
	spend := bearerport.NewPort()
	spend.Register(sCashu)
	spend.Register(sFedi)
	spend.Register(sPow)
	for _, a := range []bearerport.Authority{sCashu, sFedi, sPow} {
		if _, err := spend.IssueInto(a.Name(), 500); err != nil {
			check(err)
		}
	}
	for _, a := range []bearerport.Authority{sCashu, sFedi, sPow} {
		out, err := spend.Spend(200, a.Name())
		check(err)
		fmt.Printf("  spend(200) on %-32s -> %s (%d left)\n",
			a.Name(), clip(out), spend.BalanceByAuthority(a.Name()))
	}
	_, err = spend.Spend(10_000, sCashu.Name())
	fmt.Printf("  spend(10000)                          -> %v\n", err)
	fmt.Println()
	fmt.Println("  Namespaced target keys keep a federation id and a mint URL from ever colliding,")
	fmt.Println("  and callers depend on Instrument/Authority only: no Cashu type crosses the seam.")

	h("Result: what it would take")
	fmt.Println("  The port is already generic enough for both fedi and cashu, for one reason:")
	fmt.Println("  it does not model either. It requires of every authority exactly two memories —")
	fmt.Println("  what it signed (NUT-09) and what it consumed (NUT-07) — plus a declaration of")
	fmt.Println("  how it can answer, because a backend that cannot answer must be allowed to say")
	fmt.Println("  so instead of being guessed at.")
	fmt.Println()
	fmt.Println("  So the work is not a rewrite. It is:")
	fmt.Println("    1. the namespaced target key (cashu:<url> / fedi:<id> / pow:<miner>),")
	fmt.Println("    2. a backend-scoped Decode instead of a cashuA/cashuB parser,")
	fmt.Println("    3. one swap-based Acquire for every backend, since a bearer instrument is")
	fmt.Println("       destroyed by being spent and therefore has to be re-issued,")
	fmt.Println("    4. the declared capability matrix replacing the nut_07/nut_09 booleans,")
	fmt.Println("    5. and the two derivations above, each pinned by a test.")
}

func h(s string) {
	fmt.Println()
	fmt.Println("== " + s)
}

func stateOf(p *bearerport.Port, in bearerport.Instrument) string {
	s, err := p.CheckState(in)
	if err != nil {
		return fmt.Sprintf("%v (%v)", s, err)
	}
	return s.String()
}

func clip(s string) string {
	if len(s) > 34 {
		return s[:31] + "..."
	}
	return s
}

func check(err error) {
	if err != nil && !errors.Is(err, bearerport.ErrStateUnavailable) {
		panic(err)
	}
}
