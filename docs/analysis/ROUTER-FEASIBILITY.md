# LNURLcash (LUD-25) acceptance on a live TollGate router — feasibility & minimal-change analysis

**Target:** `tollgate-A04E`, OpenWrt 24.10.2 aarch64_cortex-a53, bench router at `/dev/ttyUSB1` @115200.
**Artifact read:** `/usr/bin/tollgate-wrt`, 12,558,496 bytes, mtime `Oct 14 11:22`.
**Source read:** `git clone --depth 1 https://github.com/OpenTollGate/tollgate-module-basic-go` → HEAD
`cf7cad34677e87e775e7f16786b1bbf18c4bc8bc` ("fix(tollwallet): bound each mint registration so a slow mint cannot stall boot (#940)", Wed Oct 14 12:43:17 2026 +0200); tree `VERSION` = `v0.6.0-rc4`.
**Method:** read-only. All router interaction under `flock` on `~/.hermes/peripheral_locks/tg-bench.lock` via `~/repos/openwrt-serial-lab/scripts/console-exec.py`. No writes, no `uci set`, no wallet operations. Lock released (re-verified free).
**Labels:** `[measured]` = real command output quoted below. `[inferred]` = derived by reading code/spec, no execution.

---

## 0. Headline

1. **The instrument type is hard-coded at three independent layers.** `[measured]` The advertisement on `:2121` publishes `["price_per_step","cashu",…]` for all six reachable mints; the handler reads the bearer instrument as a raw string; the merchant calls `NewWalletPort` **directly**, bypassing the one function that *could* have selected another backend. There is a designed seam, and it is **dead code**.
2. **The router has zero chain access** — no bitcoind, no electrum, no SPV — confirmed on-device *and* in source.
3. **However, the taproot verification primitives are already compiled into the shipped binary and unreachable.** `[measured]` `strings /usr/bin/tollgate-wrt` emits `ErrTaprootSigInvalid`, `ErrTaprootMerkleProofInvalid`, `ErrControlBlockInvalidLength`, `ErrTaprootOutputKeyParityMismatch`, `ErrWitnessMalleated`, `ErrSchnorrHashValue`, `OP_CHECKLOCKTIMEVERIFY`… These come from `btcd`'s `txscript` + `btcec/v2`, compiled in as **indirect** dependencies of the gonuts Lightning stack. Option (a) — a pure-Go in-process taproot verifier — needs **no new dependency and no new cgo**, only reachability (promote the indirect `require` to direct) plus reachable Go 1.26 code.
4. **The blocker is not cryptography, it is instrument semantics.** LUD-25's `cs1` proves *issuance*, not *ownership*; a `k1` is not a nullifier; and the spec explicitly tells a new holder to **rotate when online** — i.e. an *offline* acceptor is by construction accepting a **replayable** instrument. That collides head-on with the module's own fund-safety doctrine for exactly the reason the module already wrote it down.
5. **Recommendation: do not build an LNURLcash acceptor into `tollgate-module-basic-go`.** Details and the safe alternative in §6.

---

## 1. The payment surface as it exists today `[measured]`

### 1.1 Listeners (raw `netstat -lntp` on the box)

```
tcp  0.0.0.0:2051    LISTEN  2082/uhttpd     <- captive portal static SPA
tcp  0.0.0.0:2050    LISTEN  4398/nodogsplash
tcp  0.0.0.0:22      LISTEN  1611/dropbear
tcp  0.0.0.0:80      LISTEN  2083/uhttpd     <- router-home
tcp  0.0.0.0:8443    LISTEN  2080/uhttpd     <- admin (TLS)
tcp  0.0.0.0:8080    LISTEN  2081/uhttpd     <- admin (redirect_https)
tcp  0.0.0.0:8090    LISTEN  2080/uhttpd     <- admin SPA
tcp  0.0.0.0:443     LISTEN  2081/uhttpd
tcp  :::2121         LISTEN  3786/tollgate-wrt   <- THE PAYMENT API
```

Process table quirk worth recording: `ps w` shows **two** `:2051`-ish uhttpd instances and the money process:
```
2082 root 1592 S /usr/sbin/uhttpd -f -h /etc/tollgate/tollgate-captive-portal-site -r tollgate-A04E -n 3 -D -p 0.0.0.0:2051
3786 root 1246m S /usr/bin/tollgate-wrt
```
`[measured]` `/usr/bin/tollgate-wrt version` **fails when the service is already running**:
```
FATA[2026-10-14T22:39:02Z] Failed to start TollGate  error="bind the payment API on :2121: listen tcp :2121: bind: address already in use"
```
That is the one-command proof that **`:2121` belongs to `tollgate-wrt`** and of the bind-before-construct boot order in `src/main.go` (the API is bound in `init()` before the merchant exists).

### 1.2 `:2121` endpoint inventory — read from source and cross-checked live

`src/main.go:1799-1848` (`registerAPIHandlers`) registers **exactly six** routes plus an optional identity block:

| Route | Gates | Handler |
|---|---|---|
| `GET/POST /` | `RateLimitMiddleware(CorsMiddleware(requireStarted(HandleRoot)))` | `HandleRoot` → POST: `HandleRootPost`; else `handleDetails` |
| `GET /whoami` | `CorsMiddleware(handler)` — **not** `requireStarted` | echoes caller address, `[measured]` returns bare `mac=` from a local socket |
| `GET/POST /ln-invoice` | `CorsMiddleware(requireStarted(handleLNInvoiceRoute))` | LUD-03-style Lightning quote |
| `GET /balance` | `requireStarted(HandleBalance)` | `[measured]` `{"status":1,"session_active":false,"usage":0,"allotment":0,"remaining":0}` |
| `GET /usage` | `requireStarted(HandleUsage)` | `[measured]` `-1/-1` |
| `GET /session-state` | `requireStarted(HandleSessionState)` | `[measured]` `{"status":1,"mac":"","state":"none"}` |
| `POST /session/ticket`, `POST /session/rebind` | `CorsMiddleware` only (identity block) | memory-only session handles |
| `GET /identity`, `POST /identity/reveal-seed` | identity block; registered **only** if `identities.json[0].privatekey` parses | BIP39/npub derivation |

`src/main.go:1801-1804` is the money route, verbatim:
```go
mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
    mainLogger.WithField("remote_addr", r.RemoteAddr).Debug("Hit / endpoint")
    RateLimitMiddleware(CorsMiddleware(requireStarted(HandleRoot)))(w, r)
})
```
`src/main.go:1216-1222`:
```go
func HandleRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		HandleRootPost(w, r)
	} else {
		handleDetails(w, r)
	}
}
```

**There is no `/note-pay`, `/lnurlcash`, or any other instrument route.** `[measured]` `GET /note-pay` returns **HTTP/1.0 200 OK with the kind:10021 advertisement body (815 bytes)** — i.e. it falls through to `mux.HandleFunc("/")`'s default GET branch. So `/note-pay` is *not* a route; it is a 200 that the catch-all `"/"` pattern serves. This is a false-positive trap for any probe that only checks for a 200; a real route would not also answer `/anything-else` with the same 815 bytes.

### 1.3 The wire format of acceptance — `[measured]` `GET /` (kind:10021)

```json
{"kind":10021,"id":"7551ca8554a8c536eeaaf7b2b00203b1675d1a7ff5edc4611713fcb92d90a39a",
 "pubkey":"6a8f7e6cbf0f47a207a6b9e8f9f109b44396aab10c522516d2c3a49cc9beaf82",
 "created_at":0,
 "tags":[["metric","bytes"],["step_size","22020096"],["tips","1","2"],
   ["price_per_step","cashu","1","sat","https://mint.coinos.io","1"],
   ["price_per_step","cashu","1","sat","https://mint.minibits.cash/Bitcoin","1"],
   ["price_per_step","cashu","1","sat","https://mint.macadamia.cash","1"],
   ["price_per_step","cashu","1","sat","https://mint.westernbtc.com","1"],
   ["price_per_step","cashu","1","sat","https://kashu.me","1"],
   ["price_per_step","cashu","1","sat","https://mint.cubabitcoin.org","1"]],
 "content":"","sig":"c0f528be99ea20e391935a0d64051e557ef6817adab929318f4e34611ff9252a7dd9199b9b707b9ff092c720c05e7bfcc67ed15cb47c69f6e87ee7ac626c3510"}
```

**The instrument is named in-band and it is the literal string `"cashu"`** — field 2 of every `price_per_step` tag. A LNURLcash-aware *consumer* negotiating on this advertisement sees "cashu" and nothing else; there is no second instrument offered, and no way to advertise one without changing the tag emitter.

Also note: **6 of the 7 configured mints are advertised.** `mint.lnserver.com` (config index 2) is absent — the reachable-only filter of `mint_health_tracker.GetReachableMintConfigs()` (`src/merchant/merchant.go:494`) has excluded it. The configured `accepted_mints` array and the advertised set are *not* the same thing.

### 1.4 `GET /ln-invoice` (LUD-03 quote) — `[measured]`

```
HTTP/1.0 400 Bad Request
Access-Control-Expose-Headers: X-TollGate-Client-MAC, X-TollGate-Mac-Claim-Ignored, Retry-After
Content-Type: application/json
{"status":0,"quote":"","mint_url":"","amount":0,"state":"","access_granted":false,"error":"quote is required"}
```
(refuses without a quote id, before any mint contact — correct fail-closed behaviour.)

### 1.5 `POST /` — the actual Cashu acceptance, `[measured]`

```
POST / HTTP/1.0  →  HTTP/1.0 400 Bad Request
{"kind":21023,"id":"7672417bde…","tags":[["level","error"],["code","device-unresolved"]],
 "content":"We could not identify your device on the network. Reconnect to the TollGate Wi-Fi and try again."}
```
(from a local socket with no resolvable LAN identity — the pre-token refusal at `src/main.go:1092-1099`, exactly as designed).

The request-body contract, from `src/main.go:1045-1057`:
```go
func extractCashuToken(body []byte) (token string, event *nostr.Event) {
	var ev nostr.Event
	err := json.Unmarshal(body, &ev)
	if err == nil && ev.Kind == 21000 {
		for _, tag := range ev.Tags {
			if len(tag) >= 2 && tag[0] == "payment" { return tag[1], &ev }
		}
		return "", &ev
	}
	return strings.TrimSpace(string(body)), nil
}
```
**The body IS the bearer instrument.** `[measured]` the binary even exposes the consequence — `strings` shows `Failed to drain mint`, and `src/main.go:1111-1117` documents it in-tree:
> *"The body IS the bearer instrument on this route: … a log line carrying it is a spendable token."*

Accepted `Content-Type` is `text/plain`, `application/json`, or empty; anything else is 415 (`src/main.go:1072-1078`).

### 1.6 `/etc/tollgate/config.json` — full schema `[measured]`

```json
{ "config_version": "v0.0.10", "log_level": "info",
  "accepted_mints": [ { "url": "...", "min_balance": 64, "balance_tolerance_percent": 10,
      "payout_interval_seconds": 60, "min_payout_amount": 128, "price_per_step": 1,
      "price_unit": "sat", "purchase_min_steps": 1 }, x7 ],
  "profit_share": [ {"factor":0.79,"identity":"owner"}, {"0.07","c08r4d0r"},
                    {"0.07","amperstrand"}, {"0.07","origami74"} ],
  "step_size": 22020096, "margin": 0.1, "metric": "bytes", "show_setup": true,
  "reseller_mode": false,
  "upstream_detector": { "probe_timeout": 10000000000, "probe_retry_count": 3,
     "probe_retry_delay": 2000000000, "require_valid_signature": true,
     "ignore_interfaces": ["lo","docker0","br-lan","hostap0"], "only_interfaces": [],
     "discovery_timeout": 300000000000 },
  "upstream_session_manager": { "max_price_per_millisecond": 0.002777777778,
     "max_price_per_byte": 0.00003725782414,
     "trust": {"default_policy":"trust_all","allowlist":[],"blocklist":[]},
     "sessions": {"preferred_session_increments_milliseconds":60000,
                  "preferred_session_increments_bytes":2500000000,
                  "millisecond_renewal_offset":10000, "bytes_renewal_offset":1225000000},
     "usage_tracking": {"data_monitoring_interval":500000000} },
  "upstream_wifi": { "scan_interval_seconds":300, "fast_check_seconds":30, "lost_threshold":2,
     "hysteresis_db":12, "signal_floor":-85, "blacklist_ttl_minutes":60, "emergency_penalty":20,
     "max_consecutive_failures":3, "switch_cooldown_minutes":10, "startup_grace_seconds":90,
     "post_switch_wait_seconds":5, "dhcp_timeout_seconds":180, "manual_pause_seconds":120,
     "vendor_ie_discovery":false },
  "private_encryption": "psk2+ccmp", "admin_access": "both", "entry_ui": "board" }
```

Top-level keys at lines 2,3,4,76,94,95,96,97,98,99,113,131,147,148,149 `[measured]`.
Schema authority: `src/config_manager/config_schema.go:27` `GetConfigSchema()` — 14 top-level fields. Enum-constrained: `LogLevel` {debug,info,warn,error} (`:36`), `Metric` {bytes,milliseconds} (`:41`). `AcceptedMints` children (`:71-80`) are exactly the 8 keys seen on disk.

**Three negative findings that matter:**
- **There is no `wallet.backend` config key.** `[measured]` `grep -rniE 'wallet_backend|wallet\.backend|\"backend\"|WalletConfig|WalletPolicy' src/config_manager/ src/merchant/ src/main.go` → **no matches**. The schema has no wallet-selection field at all.
- **There is no `accepted_instruments` / instrument selector.** Instrument type is not configurable anywhere.
- **No `accepted_mints` entry can express a non-Cashu authority** — every field is a Cashu mint URL plus sat pricing.

`/etc/tollgate/install.json` `[measured]` reports `"installed_version": "0.0.0"`, `"package_path": "false"`, `"release_channel": "stable"`, `config_version v0.0.2`.

### 1.7 Where the acceptance logic actually lives

The money path is: `HandleRootPost` (`src/main.go:1060`) → resolve client MAC from the **socket** → `extractCashuToken` → `merchant.PurchaseSession(token, mac)` (`src/merchant/merchant.go:1493`) → `tollwallet.DecodeToken` (`:1507`) → `grantSessionAccess` (`:1790`). The instrument is decoded by `WalletPort.DecodeToken` (`src/tollwallet/port.go:191`) and value is realised by `WalletPort.Receive` (`:196`) — a **keyed-swap** against a Cashu mint. Cashu-ness is assumed at all four of: the advertisement tag, the raw-string body, `DecodeToken`, and `Receive`.

### 1.8 Build provenance discrepancy (recorded, not resolved)

`[measured]` the installed package reports `Version: 0.6.0_rc5-r1` (`/usr/lib/opkg/info/tollgate-wrt.control`), while the binary's own buildinfo says `v0.6.0-rc4-gcf7cad3` and carries `cf7cad34677e87e775e7f16786b1bbf18c4bc8bc` — the **exact SHA** of the clone, and `VERSION` at the clone is `v0.6.0-rc4`. So the **binary is a faithful build of `cf7cad3`** (the analysis target) and the opkg control metadata disagrees with the embedded version string. Per the module's own lesson that `apk info -v` is weaker evidence than the payload, trust the binary: **code shown in this memo matches the running bytes.** `Architecture: aarch64_cortex-a53`, `Installed-Size: 21227520`, `Depends: libc, nodogsplash, jq, ca-bundle`, `License: GPL-3.0-only`.

---

## 2. Pluggability verdict: **the seam exists in source and is UNREACHABLE on the router.**

### 2.1 What the seam is

`src/tollwallet/port.go:187` defines `WalletPort` — 20 methods, "the seam between merchant/lightning code and the underlying Cashu library (gonuts-tollgate by default, cdk-go behind the `cdk_wallet` build tag)" (`:3-5`). Adapters: `gonuts_wallet.go` (tag `!cdk_wallet`), `cdk_wallet.go` (tag `cdk_wallet`), and `sidecar.go` (`SidecarWallet`, AF_UNIX + newline-delimited JSON). `src/tollwallet/manifest.go:33` defines the capability descriptor with booleans `NUT07/NUT09/Restore/CGO` (`:25-30`); three manifests ship in-tree at `src/tollwallet/manifests/{gonuts,cdk,nucula}.json` plus a selection table `wallet-policy.json`.

`src/tollwallet/select.go:28-41` is the *intended* runtime selector:
```go
func OpenWallet(cfg WalletConfig) (WalletPort, *Manifest, error) {
	switch cfg.Backend {
	case "", "gonuts", "default", "in_process":
		p, err := NewWalletPort(cfg.Path, cfg.AcceptedMints, cfg.AllowAndSwapUntrustedMints)
		return p, nil, err
	case "cdk", "nucula", "sidecar":
		if cfg.Socket == "" { return nil, nil, fmt.Errorf("tollwallet: backend %q needs a socket", cfg.Backend) }
		return NewSidecarWallet(cfg.Socket)
	default:
		return nil, nil, fmt.Errorf("tollwallet: unknown wallet backend %q", cfg.Backend)
	}
}
```
and `policy.go:53` `SelectBackend(arch, flashMB)` reads `wallet-policy.json`, whose aarch64 rule (`manifests/wallet-policy.json`) literally selects the **CDK sidecar** for this box's arch:
```json
{ "match": { "arch": "aarch64_cortex-a53" }, "backend": "cdk",
  "socket": "/var/run/tollgate/wallet-cdk.sock", "note": "aarch64: CDK sidecar, ~4.1 MiB size-optimized" }
```

### 2.2 Why none of it fires

`[measured]` exhaustive call-site search:
```
src/merchant/merchant.go:365:  tollwallet        tollwallet.WalletPort
src/merchant/merchant.go:509:  tw, walletErr := tollwallet.NewWalletPort(walletDirPath, mintURLs, false)
```
**`OpenWallet` has exactly zero production callers.** Its only callers are `select.go:54` (inside `OpenFor`) and tests (`select_test.go`, `policy_test.go`, `policy_manifest_test.go`, `manifest_test.go`). `OpenFor` and `LoadPolicy` likewise have zero production callers. `SidecarWallet` has zero production constructors.

The merchant hard-codes the in-process gonuts adapter at `src/merchant/merchant.go:509`, and the struct field at `:365` is only *typed* as the interface:
```
509:		tw, walletErr := tollwallet.NewWalletPort(walletDirPath, mintURLs, false)
```
**Verdict:** the port is an **injectable test seam typed as an interface**, not a runtime-swappable backend. On the router today, `wallet.backend` does not exist (§1.6), the sidecar path is never taken, no daemon socket exists (`[measured]` `ls /var/run/tollgate/` → **No such file or directory**; only `/var/run/tollgate.sock`, the module's own CLI socket, and `/tmp/ndsctl.sock`), and **no wallet backend can be switched without a config-schema change plus a source change plus a rebuild.**

### 2.3 But — the seam's *shape* is what makes option (b) cheap

Two facts make a sidecar the low-friction path **if** one ever wires it:
- `src/tollwallet/sidecar.go:100` `func (s *SidecarWallet) Call(method string, params any, out any) error` is documented as *"an escape hatch for optional/extended daemon methods"* — so a daemon can expose `note_verify`/`note_receive` **without touching `WalletPort`**, and the module reaches it via `Call`. The socket path is a plain `net.DialTimeout("unix", ...)` (`:105`).
- The ambiguity vocabulary is already built: `ErrSidecarAmbiguous` (`sidecar.go:35`) and `sidecarReadOnlyMethods` (`:42-50`) separate re-issuable reads from money-movers — but **`note_verify` would not be in that map**, so a dropped response on a note-verify would surface as ambiguous-by-default, which is the *correct* fail-closed default for a replayable instrument.

Wiring the seam is *not* a one-liner: it needs a schema field, a config→`WalletConfig` mapping, and a replacement at `merchant.go:509`. `[inferred]`, judged against the module's own "one logical change per PR" rule, that is ≥3 PRs.

---

## 3. Chain-access verdict: **confirmed NONE. No chain code exists, and none runs on the device.**

### 3.1 The router has no chain software `[measured]`

```
root@tollgate-A04E:~# opkg list-installed | grep -iE 'bitcoin|electrum|btcd|lnurl|taproot|secp'
grep_rc=1                          # zero matches, out of 192 installed packages

root@tollgate-A04E:~# ls /usr/bin /usr/sbin | grep -iE 'electrum|bitcoin|btcd|lnurl|taproot'
grep_rc=1                          # zero matches
```
Available tools (probed explicitly, because a test that depends on a missing applet must fail loudly):
`wget→/usr/bin/wget`, `nc→/usr/bin/nc`, `opkg→/bin/opkg`, `uclient-fetch`, `jq→/usr/bin/jq`.
**MISSING: `curl`, `sqlite3`, `apk`, `python3`, `openssl`.**
Two operational consequences for any future probe: (i) `curl` does not exist — use `wget -qO-` or `printf … | nc`; (ii) **BusyBox `wget` has no `-S`**, so HTTP status codes need `nc` (as used in §1.4/§1.5). `sqlite3` is absent, so `wallet.db` cannot be inspected on-box.

`opkg` (not `apk`) is the package manager → the lane is `.ipk`, and the box's `Installed-Size` of 21 MB against a feed that already reports `rc5` is relevant to §4's flash budget.

### 3.2 The source has no chain code `[measured]`

```
src/valve/valve.go:71:   // SOCKET DIED ("Socket is not ready for communication : Bad file descriptor"   <- comment
src/valve/valve.go:592:  // communication : Bad file descriptor", "Could not connect to server": neither  <- comment
src/tollwallet/manifest.go:32: // Manifest is a wallet backend's capability descriptor.               <- comment
```
**Every hit for `btcd|electrum|btcwallet|neutrino|libbitcoin|txscript|utxo|txid|descriptor|hash160` in non-test `src/` is a comment or the word "descriptor".** And `[measured]`:
```
grep -rniE 'taproot|p2tr|schnorr|bech32m|xonly' src/ --include='*.go'   →  (no matches)
```
There is **no** UTXO type, no txid, no script, no address code, no chain client. Prior analysis is **confirmed, not refuted**.

### 3.3 The one surprise: the *cryptography* is already in the binary `[measured]`

```
root@tollgate-A04E:~# strings /usr/bin/tollgate-wrt | grep -o 'ErrTaproot[A-Za-z]*' | sort -u
ErrTaprootMaxSigOps / ErrTaprootMerkleProofInvalid / ErrControlBlockInvalidLength
ErrTaprootOutputKeyParityMismatch / ErrTaprootPubkeyIsEmpty / ErrTaprootSigInvalid
root@tollgate-A04E:~# strings … | grep -iE 'schnorr|secp256k1'   → ErrSchnorrHashValue, ErrPubKeyNotOnCurve, secp256k1…
root@tollgate-A04E:~# strings … | grep -E 'ScriptVerify|TaprootMaxSigOps|WitnessPubKeyType'
   → ErrScriptUnfinished, ErrNegativeLockTime, ErrWitnessMalleated, ErrWitnessPubKeyType, ErrWitnessHasNoAnnex,
     ErrWitnessProgramWrongLength, ErrDiscourageOpSuccess, ErrTaprootMaxSigOps
root@tollgate-A04E:~# strings … | grep -oE 'OP_CHECKSIG[A-Z]*'   → OP_CHECKSIGADD, OP_CHECKSIG, OP_CHECKSIGVERIFY
root@tollgate-A04E:~# strings … | grep -E '^dep\tgithub.com/(btcsuite|decred)'
dep github.com/btcsuite/btcd                      v0.24.3-0.20250318170759-4f4ea81776d6
dep github.com/btcsuite/btcd/btcec/v2             v2.3.4
dep github.com/btcsuite/btcd/btcutil              v1.1.6
dep github.com/btcsuite/btcd/btcutil/psbt         v1.1.10
dep github.com/btcsuite/btcd/chaincfg/chainhash   v1.1.0
dep github.com/btcsuite/btcwallet                 v0.16.14
dep github.com/decred/dcrd/dcrec/secp256k1/v4     v4.4.0
root@tollgate-A04E:~# strings … | grep -icE 'lnurlcash|withdrawrequest|mintpubkey|libbitcoinkernel'   → 0
root@tollgate-A04E:~# strings … | grep -cE 'lnurlp'                                                    → 1   (positive control)
```
So: **a full BIP-341/BIP-342 script-interpreter's error surface is resident in the 12.5 MB binary**, and the binary has **zero** LNURLcash surface. The positive control (`lnurlp` = 1) proves the grep methodology can find LNURL strings when they exist — the LNURLcash negative is real, not a broken probe.

`src/go.mod` lists all of the above as **`// indirect`** — pulled in by `github.com/OpenTollGate/gonuts-tollgate` (Lightning/`lnd`/`neutrino`) and `lnd v0.19.1-beta.rc1`. `src/tollwallet/go.mod` carries `cdk-go v0.17.3` as a **direct** require.

The only LNURL code in-tree is *outbound pay only* — `src/lightning/lightning.go:51 GetInvoiceFromLightningAddress` builds `https://%s/.well-known/lnurlp/%s` and asks for an invoice, with SSRF guards at `:16-33` and `:94-100`. It never *serves* a `withdrawRequest` and never verifies one. `MeltToLightning` (`src/tollwallet/port.go:225`) is the payout side.

---

## 4. Minimal change to VERIFY an LNURLcash note: options, cost, buildability

### 4.0 What "verify" costs, derived from the spec

From LUD-25 (`https://raw.githubusercontent.com/lnurl/luds/lnurlcash/25.md`):

```
leaf    = OP_SHA256 <h> OP_EQUAL                      // a8 20 <h> 87
Q       = x(lift_x(H) + tagged_hash("TapTweak", H || tapleaf_hash(leaf))·G)
control = (0xc0 | parity(Q)) || H
H       = 50929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0
```
Canonical spend tx: `nVersion=2`; `vin[0].prevout = (tagged_hash("LNURLcash/mint", domain), 0)`, empty `scriptSig`, claimed `nSequence`; `vout[0] = 0 sat, empty scriptPubKey`; claimed `nLockTime`; **spent output `(OP_1 <Q>, 0 sat)`**.
Key path: `ck1` = `Q(32) ‖ sig(64)`, `SIGHASH_DEFAULT`, verified against the canonical tx via BIP-341/BIP-342.
Script path: `cw1` = `u32 locktime ‖ u32 sequence ‖ (u16 len ‖ item)*` over `script, control, witness…`, witness stack `[*witness, script, control]`.
`cs1` = bech32m HRP carrying an amount (BOLT-11 style) + 65-byte recoverable ECDSA (r‖s‖recid) over
`sha256(sha256("Lightning Signed Message:" || "LNURLcash:" || amount_msat || ":" || lowercase_hex(Q)))`.
Short forms: bare 64-hex `preimage` (=`k1`) or 64-hex `h` (=`cp1`), accepted wherever a spend/`cp1` is expected.

So verification = **taproot output-key reconstruction (SHA256 + x-only point add + BIP-341 tagged hash) + a BIP-340/BIP-342 script interpreter run + (for `cs1`) a recoverable-ECDSA recover + a domain-scoped sighash.** All of it exists in `btcd`'s `txscript`/`btcec/v2`, which are already in the binary (§3.3).

### 4.1 The options table

| # | Option | What it needs | Runtime cost (this box) | musl aarch64 buildability | Verdict |
|---|---|---|---|---|---|
| **a1** | **Pure-Go taproot verifier, in-process** — use `btcd/txscript` + `btcec/v2` | Promote two deps from `// indirect` → direct in `src/go.mod`; add ~1 reachable package (a verifier module); no cgo | **≈0 flash, ≈0 RAM delta.** The 12.5 MB binary **already contains** every error constant and the interpreter (§3.3), so promoted-but-already-linked code adds only a small reachability delta. `[inferred]` | **GREEN.** Existing CI is `CGO_ENABLED=0` for `arm64, armv7, mipsle-softfloat, mips-softfloat, amd64` — measured in `.github/workflows/build-package.yml:238`, `.ngit/act/workflows/build-package-binaries.yml:204`, `packaging/local-build-ipk.sh:83,87`, `scripts/build-sdk-package.sh:147,152`, `scripts/repro-test.sh:124,127`. Pure-Go, so `CGO_ENABLED=0` is satisfied by construction. | **The cheapest *cryptographic* path — and the one the task framed as hard is actually the easy one.** |
| **a2** | Pure-Go, but vendored from `libbitcoinkernel` test vectors | Same as a1 + pull `25-vectors.json` into `tests/` | as a1 | GREEN | Sensible *evidence* addition to a1, not a separate option. Gives spec-conformance for free. |
| **b** | **Delegate to a sidecar** (Go/Rust daemon on `/var/run/…`) | A new daemon binary in the package (Rust+musl ≈4.1 MiB per `manifests/cdk.json`, or Go ≈similar); wiring `OpenWallet`+a config key+schema (§2.3); reach it via `SidecarWallet.Call` (`sidecar.go:100`) | **+4–6 MiB flash** to ship the daemon, **+RSS** for a second process (box has `MemTotal: 1010880 kB`, `MemAvailable: 847120 kB`, so RAM is not the constraint — flash and complexity are). Overlay is `7.2 G` with `219.8 M` used, so the 3% figure is an artifact of a loop-mounted overlay on the bench, **not** an entitlement. `[measured]` | GREEN if statically musl-linked (precedent exists: `src/tollwallet/manifests/cdk.json` claims `aarch64_cortex-a53: 4346264` bytes) | Buys **process isolation** and keeps the money bug out of the gate binary. Costs a second artifact, a socket, a supervisor unit, and the module's own `ErrSidecarAmbiguous` semantics leaking into note-verify (§2.3). |
| **c** | **Native `libbitcoinkernel` via cgo** | A musl-built C++ `libbitcoinkernel` + cgo bindings; **abandon the pure-Go build contract** | **+10–20 MiB flash** `[inferred — no musl `libbitcoinkernel` build exists on this box; not measured]`; CGO call into a C++ runtime on a 1-core MIPS-class target | **RED — this is the blocker.** `CGO_ENABLED=0` is set for the *entire* 5-arch release matrix at the six sites listed above. Adopting cgo **changes the build contract for five architectures**, and the module's own record says the existing cgo adapter (`cdk_wallet.go`) **has never once been built by CI**. Producing a musl-aarch64 `libbitcoinkernel` that *runs* (not merely links) is a from-scratch toolchain project. | **Not viable.** The task's hypothesised blocker is real, and it is the *only* one that actually blocks. |

### 4.2 The decisive finding about (a)

The task framing assumed `btcec`'s presence would have to be *checked* and that "a Go taproot/tapscript verifier in-process" was the unknown. It isn't the unknown:

1. **`btcec/v2 v2.3.4` and `btcd v0.24.3-…` are compiled into the shipped router binary today** (§3.3) — as indirect deps of the Lightning stack. So there is **no dependency addition**.
2. `btcsuite/btcd/txscript` and `btcsuite/btcd/btcec/v2/schnorr` are pure Go, and **`txscript` contains the full taproot script interpreter** — `[inferred]`, and consistent with the error surface actually present in the binary (the `ErrTaproot*`/`ErrWitness*`/`OP_CHECKSIG*` strings above are `txscript`'s, and `ErrSchnorrHashValue`/`ErrPubKeyNotOnCurve` are `btcec/v2/schnorr`'s).
3. So **(a) is roughly a `go get`-free promotion plus a verifier module**, verified against LUD-25's `25-vectors.json` (which the spec states were *"accepted by `libbitcoinkernel` against the canonical spend transaction"*).

**Therefore: the hard part of adding LNURLcash to this router is not the cryptography and not the build. It is that a `k1` is not a nullifier.** Which is §5.

---

## 5. Failure modes, mapped onto the module's own invariants

`AGENTS.md:26-35` (the doctrine, verbatim):
> *"Any change touching payments, wallets, sessions, gates, mints, payouts, retries, or persistent identifiers is a **distributed state-machine change**, not a local edit. … Every such change must state: the point of no return, what durable state is written **before** it, recovery behavior after a crash at each step, retry and duplicate-execution behavior, and the compensating action if a later step fails."*

`AGENTS.md:57-60`, `:61-68`, `:69-74`, `:75-77`, `:82-88` are the hard rules. Each failure mode below names the rule it breaks.

### 5.1 The instrument is replayable by spec, and the module's whole duplicate story assumes it is not

**Fact.** LUD-25 states, in its own warning block:
> *"A certificate proves **issuance, not that the note is still outstanding: the previous holder still knows the spend**. A new holder SHOULD rotate (or merge) the note as soon as it is online."*

And the handover sequence diagram is explicit that Alice hands Bob `lnurlw://mint.example/w?k1=A&c=cs1` and **Bob is offline** while he "derives `Q` from `A`, checks `A` opens `Q`, checks `cs1` against `mintPubkey`". The `k1` is transmitted. It is a **bearer secret at rest, held by two parties simultaneously** until — if ever — a rotation reaches the SERVICE.

**Why the module cannot absorb this.** The module's entire duplicate-safety apparatus is built on one property: *spending the instrument destroys it, atomically, at an external authority.*

`src/merchant/merchant.go:297-306`:
> *"beginReceive marks one note's Receive as in flight … A false return means the same note is already being processed … the caller must refuse WITHOUT touching the mint — the in-flight Receive may already have spent the proofs …"*

The in-flight guard is a **process-memory `map`** (`:307-321`, `receiveInFlight`), keyed by a fingerprint. `[inferred]` For LNURLcash the map is the *only* dedup that exists: with no mint contact at verify time, a restart clears `receiveInFlight`, and the identical `k1` re-verifies. There is no external authority to say "that `Q` is burned".

**Invariant broken:** `AGENTS.md:75-77` — *"Retries must be idempotent or use fresh state. **Concurrent duplicate submissions of one token must not double-count**."* Also `:69-74` — *"Ambiguous network results must be reconciled, never blindly retried"* — because the reconciler's evidence source (NUT-07 `/checkstate`) **does not exist for a note**: LUD-25's "Checking a note without exposing it" (`?p=<cp1>`) is a *value lookup*, not a *spentness query*.

### 5.2 `cs1` proves issuance, not ownership — the exact confusion the wallet-port design already bans

`AGENTS.md` doesn't only warn generically; the repo has already reasoned this out for the analogous case. The `bearer-instrument-wallet-port` design record states the rule as the load-bearing one:

> **"A NUT-07 answer is about CONSUMPTION, never EXISTENCE. … a plain Cashu mint answers `UNSPENT` for a nullifier it has never seen … So `UNSPENT` means 'not consumed', NOT 'real'. Existence is answered from the signing log … Conflating the two is exactly how a forged note passes a naive 'is it spendable?' check."**

`cs1` is *pure existence* (a mint's ECDSA signature over `Q` and an amount) and *zero consumption*. LUD-25 §Offline verification step 3 is literally: *"rebuilds `message` …, recovers a key from `cs1`, and compares it with the `mintPubkey` on record."* **A verifier that checks `cs1` and grants is the "naive is-it-spendable check" this repo already documented as a known trap.**

**Invariant broken:** `AGENTS.md:57-60` — *"**Do not perform irreversible monetary operations … before validations that can be done locally**"* — is *satisfied* locally and *violated* in spirit: the local validation (`cs1`) is not a validation of the property that matters. And `AGENTS.md:61-68`: granting on a `cs1` check means the irreversible operation (gate-open / value credited) has **no authority** behind it to reconcile against.

### 5.3 The failure is in the *reverse* direction from Cashu, so the compensating action is undefined

For Cashu, the ordering is `Receive` (irreversible, external) → `session` → `gate-open`. `AGENTS.md:62-68`:
> *"`Receive → session → gate-open → response` is a business transaction spanning the wallet, memory, NDS, and the HTTP client. … When the gate fails after a successful Receive, the value is in the operator wallet and the customer has nothing (#258, #403 — decide refund vs late-grant explicitly; do not silently drop it)."*

The module **has** a durable record for exactly that: the receive-intent journal (#502), written *before* the money moves —
`src/merchant/merchant.go:1584`:
> *"The receive-intent journal (#502): the attempt is durable before the money moves, so a crash, a dropped response or a restart can still be reconciled into service-or-evidence."*
with `beginReceiveIntent` (`src/merchant/receive_intent.go:161`), `resolveReceiveIntent` (`:207`), `reconcileReceiveIntent` (`:247`).

For LNURLcash there is **no compensating action available at all**:
- There is nothing to refund. `[inferred]` The router would hold `k1` — and so does the previous holder.
- There is nothing to re-check. `reconcileReceiveIntent` → `CheckTokenSpent` → NUT-07. `src/tollwallet/port.go:247-256` defines `CheckTokenSpent` around *"A receive-swap consumes all input proofs atomically at the mint"*. A note has **no mint state to query**: LUD-25's own burn rule is *"**Burns are permanent:** a burned `Q` MUST NOT be returned to the outstanding set"* — burn state exists **only at the SERVICE**, and the router is not the SERVICE.
- Worse, the attempt cannot even be *journaled* usefully. `[inferred]` `reference := receiveReference(token)` (`merchant.go:1573`) fingerprints the **serialized token** — which for a note is the `k1`, i.e. the spend. `utils.TokenFingerprint` (`src/utils/token_fingerprint.go:48-57`) is an HMAC-SHA256 with a **per-install salt at `/etc/tollgate/token-fingerprint.salt`**, derived from `strings.TrimSpace(token)`. A note's rotation (spec-mandated for a new holder!) changes `k1` completely, so the journal's key is **not stable across the rotation the spec tells the holder to perform**. And the salt itself can fail to persist (`token_fingerprint.go:82-89`: *"fingerprints will not match across a restart, which matters to the journal"*) — so even the same `k1` twice may journal under two keys.

**Invariant broken:** `AGENTS.md:61-68` (durable forward recovery or compensating action) and `:82-88` (*"Anything that affects money, access or recovery must either be persisted or reconciled from an external authority … and today it is not, which is a known gap"*). LNURLcash at the router has **neither** a durable forward recovery **nor** an external authority.

### 5.4 The `#498` / `#719` ambiguous-outcome class is *structurally worse*, not merely analogous

The module's ambiguity vocabulary is precise. `src/merchant/merchant.go:1906-1919`:
> *"`isAmbiguousMintOutcomeError` reports whether a `Receive` error leaves the fate of the note undecided … It is deliberately the inverse of the explicit-refusal list rather than a list of ambiguous shapes: only the mint can establish that it did not take the note, and an error return cannot. … it treats anything unrecognised as ambiguous too: the hedged record costs the operator one balance check, while the confident one costs the customer their note, which the mint then refuses as already spent (**#498**)."*

And the late-outcome path (`merchant.go:237-275`) has **three** dispositions, all of which require the mint:
- ambiguous → `m.reconcileReceiveIntent(reference)` (`:253`)
- definitive refusal → `resolveReceiveIntent(…, intentStateAbandoned, …)` — *"the customer keeps a spendable note"* (`:258`, comment `:246-249`)
- late **success** → *"the value is recoverable by the operator (NUT-09 restore) and the customer is owed service"* → `recordOwedGrant` (`:266-274`)

Contrast the note. `[inferred]` For LNURLcash at an offline router:
- **Definitive refusal is unreachable.** There is no authority to return one. Every outcome is "we computed a signature check that passed" — a *local* answer with no counterparty.
- **NUT-09 restore has no analogue.** LUD-25's recovery story is LUD-26 `cx1` watch-only export for *key-path* notes — which recovers *the holder's own* notes, not the *acceptor's* liability.
- **A repeat submission is indistinguishable from a second sale, permanently.** In the Cashu path the second attempt is refused by the mint as already-spent (that is the *entire* basis of the customer-facing message at `:1577`: *"a second attempt will be refused as already spent"*). For a note there is no such referee.
- **§1.8's instrument tag makes it worse in-band.** The advertisement's `["price_per_step","cashu",…]` is signed into the kind:10021 event. `[inferred]` A reseller/client that trusts the advertisement is being told the accepted instrument; relaying a note under a "cashu" tag would be a **signed statement the router cannot honour** — the exact class of artifact-level misrepresentation this repo treats as a release blocker elsewhere.

### 5.5 Timing / locktime claims are the SERVICE's clock, and the module has no such clock contract

LUD-25 §Timelocks:
> *"**SERVICE** MUST reject a script-path spend whose non-zero `nLockTime` is in the future, or whose `nSequence` enables a relative lock that has not yet elapsed **since `SERVICE` credited the note**"* … and the spec's own warning: *"A timelock honored by `SERVICE` is `SERVICE` asserting its own clock. It is a **custodial policy, not a consensus guarantee, and MUST NOT be presented as trustless.**"*

`[inferred]` "since SERVICE credited the note" requires the router to have credited it — i.e. a router that accepts notes becomes a **mint-by-another-name**, with a hot wallet's solvency exposure. That is out of scope for the module by its own org separation (OpenTollGate = protocol, strictly non-profit) and is a 7–12 day build on its own per prior analysis.

### 5.6 Summary table

| # | Failure mode | Module invariant violated | file:line |
|---|---|---|---|
| 5.1 | `k1` is a bearer secret held by ≥2 parties; the identical spend re-verifies after a restart (no mint referee, dedup is process memory) | "Retries must be idempotent or use fresh state"; "concurrent duplicate … must not double-count" | `AGENTS.md:75-77`; `merchant.go:297-306`, `:307-321` |
| 5.2 | `cs1` proves ISSUANCE not OWNERSHIP — the documented "naive is-it-spendable check" | local-validation-before-irreversible (#403/#409) is satisfied only in form | `AGENTS.md:57-60`; `port.go:247-256` |
| 5.3 | No compensating action: nothing to refund, nothing to query, journal key unstable across the spec-mandated rotation and across a salt-persist failure | durable forward recovery **or** compensating action | `AGENTS.md:61-68`, `:82-88`; `merchant.go:1584`, `:1790-1808`; `token_fingerprint.go:48-57`, `:82-89` |
| 5.4 | Every outcome is a local signature check ⇒ "outcome unknown" is *always* possible and *never* resolvable; NUT-09 restore has no analogue | "Ambiguous network results must be reconciled, never blindly retried" (#498/#719/#502) | `AGENTS.md:69-74`; `merchant.go:1906-1919`, `:237-275` |
| 5.5 | Timelock honours require a SERVICE clock + credited-note ledger ⇒ router becomes a custodian | org scope; not a module capability | `AGENTS.md:26-35`; LUD-25 §Timelocks warning |
| 5.6 | A signed kind:10021 advertises `"cashu"`; honouring a note under it is an unfunded signed claim | artifact/client honesty | `[measured]` §1.3; `main.go:1030-1040` |

---

## 6. Recommendation

**Do not put an LNURLcash acceptor inside `tollgate-module-basic-go`.** Build it as a **separate sidecar SERVICE**, and (if anything lands in this module at all) land only the *enabling* refactor, behind no instrument-specific code.

Reasons, in priority order:

1. **Correctness, not effort, decides this.** §5 shows the module has no mechanism to make a `k1` a nullifier. Every route to a nullifier requires an authority that is not the router: either the **SERVICE** (authoritative `Q`→value + burn state, per LUD-25) or a chain/UTXO view (which does not exist here — §3, confirmed on-device). Adding one inside the gate binary would mean the module becomes the mint — out of scope and a customs liability it does not want.
2. **A sidecar is where the spec already puts the responsibility.** LUD-25 is written as a `SERVICE`↔`WALLET` protocol. "SERVICE" is a role, and a TollGate that accepted notes would *be* one. Implementing it in-process just relocates the SERVICE into the money binary.
3. **The gate binary should not grow a new trust boundary.** A verifier that can be reached from the captive portal is reachable from pre-auth clients (`nodogsplash` `users_to_router` already permits `tcp port 2121` `[measured]`). Putting a novel signature-verification parser behind that surface, in the process that owns the money path, is a strict increase in blast radius for zero protocol gain.
4. **The enabling refactor is genuinely useful and is a prerequisite for any future instrument — but do it instrument-agnostically.** The `WalletPort` seam (§2) is *designed* for this and **unreachable**. Actually wiring it — schema field + config→`WalletConfig` mapping + replacing the hard-coded `NewWalletPort` at `src/merchant/merchant.go:509` — is the correct first PR, and it pays off for the CDK migration this repo already wants (the shipped `wallet-policy.json` already says aarch64 should be on the CDK sidecar). It is also the *only* change here that makes a bearing-instrument question answerable at all.

**If the operator wants the capability anyway**, the defensible minimal shape is:

- **Slice 0 (this module):** wire the `WalletPort` seam. `[inferred]` ≈3 PRs (schema field; `WalletConfig` plumbing at `merchant.go:509`; sidecar socket lifecycle). **No instrument-specific code.**
- **Slice 1 (a new repo):** an LNURLcash `SERVICE` sidecar (Go, `net/http` + `btcec/v2` — the deps are already proven to cross-compile for this matrix). Because it is the SERVICE it owns the `Q`→value ledger and the burn set, which is what makes a `k1` verifiable *and* non-replayable. Expose it over the sidecar RPC (`sidecar.go:100` `Call`) as `note_verify` / `note_receive`, and **leave both OUT of `sidecarReadOnlyMethods`** (`sidecar.go:42-50`) so a dropped response is `ErrSidecarAmbiguous` — fail-closed, per the module's own vocabulary.
- **Slice 2 (this module, only if Slice 1 ships):** add the second `price_per_step` instrument tag (this is a **wire-format change to a signed kind:10021 advertisement**, so it needs its own compatibility discussion — existing clients compare the tag positionally), and route `POST /` bodies that match a note prefix to `Call("note_receive", …)`.
- **Under no circumstances** ship Slice 2 before Slice 1's burn set is durable, because that is the *only* thing standing between the router and a replayable instrument.

**Evidence that would change this verdict:** a chain view at the gateway (light client / UTXO oracle) — that would make a note a *bonded* instrument rather than a signature, since a taproot spend is a real on-chain event. Nothing in this module or this router provides one today, and §4.1(c) shows the cgo route to `libbitcoinkernel` breaks the five-arch `CGO_ENABLED=0` build contract.

---

## Appendix A — raw evidence index

| Claim | Source |
|---|---|
| No chain pkgs/bins on device | `opkg list-installed \| grep -iE 'bitcoin\|electrum\|btcd\|lnurl\|taproot\|secp'` → `grep_rc=1`; same for `ls /usr/bin /usr/sbin` |
| Taproot interpreter resident in binary | `strings /usr/bin/tollgate-wrt \| grep -o 'ErrTaproot[A-Za-z]*'` → 6 constants; `grep -oE 'OP_CHECKSIG[A-Z]*'` → 3 |
| btcd/btcec/btcwallet/neutrino deps in binary | `strings … \| grep -E '^dep\tgithub.com/(btcsuite\|decred)'` → 7 module lines w/ versions+hashes |
| Zero LNURLcash surface in binary | `strings … \| grep -icE 'lnurlcash\|withdrawrequest\|mintpubkey\|libbitcoinkernel'` → `0` (positive control `lnurlp` → `1`) |
| Wallet seam unreachable | `grep -rn 'OpenWallet\|OpenFor\|NewSidecarWallet\|WalletConfig' --include='*.go'` → only `select.go`/tests; merchant uses `NewWalletPort` at `merchant.go:509` |
| No wallet backend config key | `grep -rniE 'wallet_backend\|wallet\.backend\|"backend"' src/config_manager/ src/merchant/ src/main.go` → no matches |
| No sidecar socket exists | `ls -la /var/run/tollgate/` → `No such file or directory` |
| Routes | `src/main.go:1799-1848`; live `netstat -lntp`; `GET /ln-invoice` → 400 JSON; `POST /` → kind:21023 device-unresolved |
| `/note-pay` is not a route | `GET /note-pay` → 200 with the same 815-byte kind:10021 body as `/` (catch-all `"/"` pattern) |
| `CGO_ENABLED=0` across 5-arch matrix | `.github/workflows/build-package.yml:238`; `.ngit/act/workflows/build-package-binaries.yml:204`; `packaging/local-build-ipk.sh:83,87`; `scripts/build-sdk-package.sh:147,152`; `scripts/repro-test.sh:124,127` |
| BusyBox gaps | `curl`, `sqlite3`, `apk`, `python3`, `openssl` MISSING; `wget` has no `-S` |
| Box resources | `MemTotal: 1010880 kB`, `MemAvailable: 847120 kB`, Swap 0; `/overlay 7.2G 219.8M 3%` (loop-mounted — not an entitlement) |
| Build provenance | binary `v0.6.0-rc4-gcf7cad3` + `cf7cad34677e87e775e7f16786b1bbf18c4bc8bc`; clone HEAD identical; `VERSION`=`v0.6.0-rc4`; opkg control says `0.6.0_rc5-r1` (discrepancy, §1.8) |
| LUD-25 text | `https://raw.githubusercontent.com/lnurl/luds/lnurlcash/25.md`, 256 lines, §"Offline verification", §"Timelocks", §"Rules", §"Test Vectors" |

## Appendix B — artifacts written

- `/home/c08r4d0r/reports/lnurlcash-tollgate/ROUTER-FEASIBILITY.md` (this memo)
- `/tmp/tmbg/` — shallow clone of `OpenTollGate/tollgate-module-basic-go` @ `cf7cad3`
- `/tmp/tg_probe{,2,3,4,5,6}.txt` + `/tmp/tg_probe*_raw.log` — the exact command scripts and raw UART transcripts
- `/tmp/lud25.md` — local copy of the LUD-25 draft spec

Router state: **unmodified.** Bench lock `~/.hermes/peripheral_locks/tg-bench.lock` taken and released (re-verified free; mtime `00:45`). `wallet.db` untouched; no `uci set`; no package install; no HTTP POST to any external host.
