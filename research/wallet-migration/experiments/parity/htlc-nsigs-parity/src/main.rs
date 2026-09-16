//! T15 NUT-14 HTLC `n_sigs`-omission parity test.
//!
//! The gonuts fork's security fix `296c7bf` closed an **HTLC signature-
//! enforcement bypass**: an HTLC proof with `pubkeys` set but `n_sigs`
//! **omitted** (the default `0`) was spendable with the *preimage alone* -- no
//! signature required. The fix defaults the required signatures to 1 whenever
//! pubkeys are present.
//!
//! This drives the *candidate* wallet's verifier, `cashu::Proof::verify_htlc`
//! (the direct analogue of gonuts' `VerifyHTLCProof`), over the same inputs and
//! prints each result. It is a standalone crate so it can exercise the exact
//! `cashu` revision under review without touching it.
//!
//! Cases:
//!   A. pubkeys present, n_sigs omitted, correct preimage, NO signature
//!      -> MUST be rejected (this is the 296c7bf bypass; pre-fix gonuts: Ok).
//!   B. same secret, valid signature by the required key
//!      -> MUST be accepted (the condition is satisfiable).
//!   C. no pubkeys, correct preimage only
//!      -> MUST be accepted (preimage-only HTLC is legitimate).
//!   D. pubkeys present, n_sigs explicitly 0
//!      -> MUST be rejected (CDK is fail-closed at construction/parse).

use std::str::FromStr;

use cashu::nuts::nut00::Witness;
use cashu::nuts::nut10::{Conditions, Kind};
use cashu::nuts::nut14::HTLCWitness;
use cashu::nuts::{Nut10Secret, Proof};
use cashu::secret::Secret as SecretString;
use cashu::{SecretData, SecretKey};

// preimage = 32 bytes of 0x42; hash = sha256(preimage).
const PREIMAGE_HEX: &str = "4242424242424242424242424242424242424242424242424242424242424242";
const HASH_HEX: &str = "425ed4e4a36b30ea21b90e21c712c649e8214c29b7eaf68089d1039c6e55384c";
const KEYSET_ID: &str = "00deadbeef123456";
const C_HEX: &str = "02a9acc1e48c25eeeb9289b5031cc57da9fe72f3fe2861d264bdc074209b107ba2";

fn htlc_proof(conditions: Option<Conditions>, witness: Option<Witness>) -> Proof {
    let nut10_secret =
        Nut10Secret::new(Kind::HTLC, SecretData::new(HASH_HEX.to_string(), conditions));
    let secret: SecretString = nut10_secret.try_into().expect("build htlc secret");
    Proof {
        amount: cashu::Amount::ONE,
        keyset_id: cashu::nuts::nut02::Id::from_str(KEYSET_ID).expect("keyset id"),
        secret,
        c: cashu::nuts::nut01::PublicKey::from_hex(C_HEX).expect("c pubkey"),
        witness,
        dleq: None,
        p2pk_e: None,
    }
}

fn htlc_witness(signatures: Option<Vec<String>>) -> Option<Witness> {
    Some(Witness::HTLCWitness(HTLCWitness {
        preimage: PREIMAGE_HEX.to_string(),
        signatures,
    }))
}

fn main() {
    let sk = SecretKey::generate();
    let pk = sk.public_key();

    // A -- the 296c7bf bypass shape: pubkeys present, n_sigs omitted, no sigs.
    let conditions = Conditions {
        pubkeys: Some(vec![pk]),
        num_sigs: None,
        ..Default::default()
    };
    let proof_a = htlc_proof(Some(conditions.clone()), htlc_witness(None));
    let a = proof_a.verify_htlc();

    // B -- same secret, correct signature over THAT proof's secret bytes (the
    // NUT-10 nonce is freshly generated per construction, so sign the very
    // secret string that will be verified).
    let mut proof_b = htlc_proof(Some(conditions.clone()), None);
    let signature = sk
        .sign(proof_b.secret.as_bytes())
        .expect("sign htlc secret");
    proof_b.witness = htlc_witness(Some(vec![signature.to_string()]));
    let b = proof_b.verify_htlc();

    // C -- control: preimage-only HTLC (no pubkeys).
    let proof_c = htlc_proof(None, htlc_witness(None));
    let c = proof_c.verify_htlc();

    // D -- explicit n_sigs = 0 with pubkeys.
    let proof_d = htlc_proof(
        Some(Conditions {
            pubkeys: Some(vec![pk]),
            num_sigs: Some(0),
            ..Default::default()
        }),
        htlc_witness(None),
    );
    let d = proof_d.verify_htlc();

    let fmt = |r: &Result<(), cashu::nuts::nut14::Error>| match r {
        Ok(()) => "Ok".to_string(),
        Err(e) => format!("Err({e:?})"),
    };

    println!("candidate cdk revision:");
    let mut failed = false;
    let mut check = |name: &str, r: &Result<(), cashu::nuts::nut14::Error>, want_ok: bool| {
        let got_ok = r.is_ok();
        let pass = got_ok == want_ok;
        failed |= !pass;
        println!(
            "  {name:<52} -> {:<34} want {:<4} {}",
            fmt(r),
            if want_ok { "Ok" } else { "Err" },
            if pass { "PASS" } else { "FAIL" }
        );
    };

    check("A pubkeys+n_sigs omitted, no signature (BYPASS)", &a, false);
    check("B pubkeys+n_sigs omitted, valid signature", &b, true);
    check("C no pubkeys, preimage only", &c, true);
    check("D pubkeys+n_sigs=0", &d, false);

    println!(
        "{}",
        if failed {
            "VERDICT: FAIL"
        } else {
            "VERDICT: PASS -- CDK reproduces the 296c7bf enforcement (default 1 sig when pubkeys present; n_sigs=0 rejected)"
        }
    );
    std::process::exit(if failed { 1 } else { 0 });
}
