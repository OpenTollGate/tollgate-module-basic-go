/*
 * nucula_core_harness.cpp — minimal runnable Linux harness around nucula's
 * wallet/protocol core, for the OpenWrt port spike.
 *
 * Part of the nucula OpenWrt port spike. OUR code (glue only). It contains no
 * nucula source: it calls nucula's own public entry points and its own
 * on-device self-tests, which is exactly what makes the measurements below
 * evidence rather than estimates.
 *
 * Modes:
 *   selftest                      run the four on-device test suites that ship
 *                                 inside nucula's core; exit 0 only if all pass
 *   measure [--proofs N] [--hold S]
 *                                 seed a wallet slot with N synthetic proofs
 *                                 through nucula's own NVS writer, reload it,
 *                                 report RSS / threads / NVS bytes written,
 *                                 then optionally hold S seconds so an external
 *                                 script can sample /proc/<pid>/smaps_rollup
 *
 * Measurement note: RSS and thread counts are read from /proc/self/{status}
 * and are therefore *process* facts, not estimates. They are only meaningful
 * for the core because the harness links nothing else.
 */
#include "cashu.hpp"
#include "cashu_json.hpp"
#include "cashu_cbor.hpp"
#include "selftest.hpp"
#include "crypto_test.h"
#include "wallet.hpp"
#include "wallet_store.hpp"
#ifdef HARNESS_PROOF_STORE
#include "wallet_internal.hpp"   /* cashu::proof_store:: — the storage layer */
#endif
#include "nvs.h"
#include "esp_log.h"
#include "esp_random.h"
#include "esp_timer.h"
#include "secp256k1.h"

#include <algorithm>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <iterator>
#include <string>
#include <vector>
#include <unistd.h>

#undef TAG   /* wallet_internal.hpp defines TAG for the wallet translation units */
#define TAG "harness"

/* Synthetic keyset id, same 33-byte/32-byte shapes as a real proof. Not a
 * secret: it is the id of a made-up keyset. */
static const char *kProofId =
    "0184237e63ce3423df7db2dcedc7329cff722a12b90206db53185fc31a4ca5ed96";  // pragma: allowlist secret (synthetic keyset id, test fixture)

/* ------------------------------------------------------------------ */
/* /proc measurement helpers                                          */
/* ------------------------------------------------------------------ */

static long proc_status_kb(const char *field)
{
    FILE *f = fopen("/proc/self/status", "r");
    if (!f) return -1;
    char line[256];
    long val = -1;
    size_t fl = strlen(field);
    while (fgets(line, sizeof line, f)) {
        if (strncmp(line, field, fl) == 0) {
            val = strtol(line + fl, NULL, 10);
            break;
        }
    }
    fclose(f);
    return val;
}

static long proc_threads(void) { return proc_status_kb("Threads:"); }
static long rss_kb(void)       { return proc_status_kb("VmRSS:"); }
static long hwm_kb(void)       { return proc_status_kb("VmHWM:"); }
static long vmsize_kb(void)    { return proc_status_kb("VmSize:"); }

/* ------------------------------------------------------------------ */
/* Synthetic wallet state                                             */
/* ------------------------------------------------------------------ */

/* Build `n` synthetic proofs. The C values are random 33-byte compressed
 * points and the secrets random 32-byte hex: the persistence path does not
 * verify signatures (only spend/verify flows do, and those need a mint), so
 * this fills the wallet with data of the same SHAPE and SIZE as real proofs —
 * which is what RSS and NVS-bytes-per-payment depend on. */
static std::vector<cashu::Proof> synth_proofs(int n, const std::string &id)
{
    std::vector<cashu::Proof> v;
    v.reserve((size_t)n);
    for (int i = 0; i < n; i++) {
        cashu::Proof p;
        p.id = id;
        p.amount = (i % 2) ? 8 : 1;
        unsigned char rnd[33];
        esp_fill_random(rnd, sizeof rnd);
        rnd[0] = 0x02;
        char hex[67];
        static const char *H = "0123456789abcdef";
        for (int k = 0; k < 33; k++) {
            hex[k * 2] = H[rnd[k] >> 4];
            hex[k * 2 + 1] = H[rnd[k] & 0xf];
        }
        hex[66] = '\0';
        p.C = hex;
        char sec[65];
        esp_fill_random(rnd, 32);
        for (int k = 0; k < 32; k++) {
            sec[k * 2] = H[rnd[k] >> 4];
            sec[k * 2 + 1] = H[rnd[k] & 0xf];
        }
        sec[64] = '\0';
        p.secret = sec;
        v.push_back(std::move(p));
    }
    return v;
}

/* Write the slot's state exactly the way Wallet::save_proofs()/save_mint_url()
 * do, but through the shim's NVS API directly, so the harness can create a
 * realistic store without network access. */
static bool seed_slot(int slot, const char *url, const std::vector<cashu::Proof> &proofs)
{
    nvs_handle_t h;
    if (nvs_open("wallet", NVS_READWRITE, &h) != ESP_OK) return false;
    char key[16];
    snprintf(key, sizeof key, "url_%d", slot);
    nvs_set_str(h, key, url);

    std::string blob = cashu::proofs_to_json(proofs);
    if (blob.empty()) { nvs_close(h); return false; }
    snprintf(key, sizeof key, "proofs_%d", slot);
    esp_err_t e = nvs_set_blob(h, key, blob.data(), blob.size());
    e = nvs_commit(h);
    nvs_close(h);
    printf("seeded slot %d: %zu proofs, JSON blob %zu bytes\n",
           slot, proofs.size(), blob.size());
    return e == ESP_OK;
}

/* ------------------------------------------------------------------ */
/* Modes                                                              */
/* ------------------------------------------------------------------ */

static int do_selftest(void)
{
    secp256k1_context *ctx = secp256k1_context_create(
        SECP256K1_CONTEXT_SIGN | SECP256K1_CONTEXT_VERIFY);
    if (!ctx) { printf("FATAL: secp256k1_context_create failed\n"); return 2; }

    int failures = 0;
    struct { const char *name; int (*run)(const secp256k1_context *); } suites[] = {
        { "crypto (NUT-00/11/12/13 vectors)", crypto_run_tests },
    };
    for (auto &s : suites) {
        printf("== %s ==\n", s.name);
        int rc = s.run(ctx);   /* 1 == pass in nucula's convention */
        printf("-- %s: %s --\n", s.name, rc ? "PASS" : "FAIL");
        if (!rc) failures++;
    }

    printf("== pure codec selftests (hex/base64url/split/NUT-10/CBOR V4) ==\n");
    bool pure = nucula_pure_selftests();
    printf("-- pure codec selftests: %s --\n", pure ? "PASS" : "FAIL");
    if (!pure) failures++;

    printf("== wallet math selftests (NUT-02 fee, selection, units) ==\n");
    bool wmath = cashu::Wallet::run_tests();
    printf("-- wallet math selftests: %s --\n", wmath ? "PASS" : "FAIL");
    if (!wmath) failures++;

    printf("== JSON parse contract selftests ==\n");
    bool js = cashu::cashu_json_run_tests();
    printf("-- JSON parse contract selftests: %s --\n", js ? "PASS" : "FAIL");
    if (!js) failures++;

    secp256k1_context_destroy(ctx);
    printf("SELFTEST_RESULT suites=4 failures=%d\n", failures);
    return failures == 0 ? 0 : 1;
}

static int do_measure(int nproofs, int hold_s)
{
    int64_t t0 = esp_timer_get_time();

    secp256k1_context *ctx = secp256k1_context_create(
        SECP256K1_CONTEXT_SIGN | SECP256K1_CONTEXT_VERIFY);
    if (!ctx) { printf("FATAL: secp256k1_context_create failed\n"); return 2; }

    if (nvs_flash_init() != ESP_OK) { printf("FATAL: nvs init\n"); return 2; }

    long rss0 = rss_kb();
    printf("baseline (post-context, pre-store): RSS=%ld kB threads=%ld\n",
           rss0, proc_threads());

    nucula_nvs_reset_stats();
    std::vector<cashu::Proof> proofs =
        synth_proofs(nproofs, "0184237e63ce3423df7db2dcedc7329cff722a12b90206db53185fc31a4ca5ed96");  // pragma: allowlist secret (synthetic proof id, test fixture)
    if (!seed_slot(0, "https://mint.example", proofs)) {
        printf("FATAL: seed_slot failed\n");
        return 2;
    }
    uint64_t seeded_bytes = nucula_nvs_bytes_written();

    /* Restore through nucula's own loader — the same call wallet_store_init()
     * makes at boot on the device. */
    long rss_before_load = rss_kb();
    cashu::Wallet w("https://mint.example", ctx, 0);
    bool loaded = w.load_from_nvs();
    long rss_after_load = rss_kb();

    printf("wallet.load_from_nvs() = %s, proofs loaded = %zu, "
           "balance = %lld sat\n",
           loaded ? "true" : "false", w.proofs().size(),
           (long long)w.balance());

    /* Exercise wallet_store: creates the recursive mutex and restores every
     * persisted slot. This is the only lock the core uses. */
    bool store_ok = wallet_store_init(ctx);
    printf("wallet_store_init() = %s, slots = %d\n",
           store_ok ? "true" : "false", wallet_store_count());

    /* NVS write cost of one full proof-blob mutation: the unit of flash cost
     * per payment in nucula's storage model (whole blob rewritten). */
    nucula_nvs_reset_stats();
    {
        nvs_handle_t h;
        nvs_open("wallet", NVS_READWRITE, &h);
        std::string blob = cashu::proofs_to_json(w.proofs());
        nvs_set_blob(h, "proofs_0", blob.data(), blob.size());
        nvs_commit(h);
        nvs_close(h);
    }
    uint64_t rewrite_bytes = nucula_nvs_bytes_written();

    int64_t t1 = esp_timer_get_time();

    printf("---- MEASURED ----\n");
    printf("nvs_dir                 = %s\n", nucula_nvs_dir());
    printf("proofs                  = %d\n", nproofs);
    printf("rss_baseline_kb         = %ld\n", rss0);
    printf("rss_before_load_kb      = %ld\n", rss_before_load);
    printf("rss_after_load_kb       = %ld\n", rss_after_load);
    printf("rss_now_kb              = %ld\n", rss_kb());
    printf("vm_hwm_kb               = %ld\n", hwm_kb());
    printf("vm_size_kb              = %ld\n", vmsize_kb());
    printf("threads                 = %ld\n", proc_threads());
    printf("nvs_bytes_seed          = %llu\n", (unsigned long long)seeded_bytes);
    printf("nvs_bytes_one_rewrite   = %llu\n", (unsigned long long)rewrite_bytes);
    printf("wallet_ready_us         = %lld\n", (long long)(t1 - t0));

    printf("MEASURE_RESULT {\"proofs\":%d,\"threads\":%ld,\"rss_kb\":%ld,"
           "\"vm_hwm_kb\":%ld,\"vm_size_kb\":%ld,"
           "\"nvs_bytes_seed\":%llu,\"nvs_bytes_one_rewrite\":%llu,"
           "\"wallet_ready_us\":%lld}\n",
           nproofs, proc_threads(), rss_kb(), hwm_kb(), vmsize_kb(),
           (unsigned long long)seeded_bytes, (unsigned long long)rewrite_bytes,
           (long long)(t1 - t0));
    fflush(stdout);

    if (hold_s > 0) {
        printf("HOLDING pid=%d for %d s (sample /proc/%d/smaps_rollup now)\n",
               (int)getpid(), hold_s, (int)getpid());
        fflush(stdout);
        sleep((unsigned)hold_s);
    }

    secp256k1_context_destroy(ctx);
    return 0;
}

/* ------------------------------------------------------------------ */
/* Storage-layer measurement and consistency tests                    */
/*                                                                    */
/* spend      — the real wallet path (Wallet::remove_proofs) on a     */
/*              seeded 200-proof wallet; compiles against any tree.   */
/* pay        — one payment (3 inputs spent, 3 change outputs landed) */
/*              through the storage layer, repeated, plus round-trip  */
/*              checks. Needs the per-proof store (-DHARNESS_PROOF_STORE=1). */
/* storetest  — the correctness suite: round trips, the "unchanged    */
/*              save writes nothing" claim, slot-mark bound, and an   */
/*              EXHAUSTIVE interruption sweep (every save can be cut  */
/*              short after op k, for every k) asserting the          */
/*              no-proof-loss invariant old∩new ⊆ S ⊆ old∪new.        */
/* ------------------------------------------------------------------ */

static std::vector<std::string> canon(const std::vector<cashu::Proof> &v)
{
    std::vector<std::string> out;
    out.reserve(v.size());
    for (const auto &p : v) out.push_back(cashu::serialize(p));
    std::sort(out.begin(), out.end());
    return out;
}

static bool multiset_in(const std::vector<std::string> &sub,
                        const std::vector<std::string> &super)
{
    return std::includes(super.begin(), super.end(), sub.begin(), sub.end());
}

static std::vector<std::string> multiset_and(const std::vector<std::string> &a,
                                             const std::vector<std::string> &b)
{
    std::vector<std::string> out;
    std::set_intersection(a.begin(), a.end(), b.begin(), b.end(),
                          std::back_inserter(out));
    return out;
}

static std::vector<std::string> multiset_or(const std::vector<std::string> &a,
                                            const std::vector<std::string> &b)
{
    std::vector<std::string> out;
    std::set_union(a.begin(), a.end(), b.begin(), b.end(), std::back_inserter(out));
    return out;
}

static secp256k1_context *make_ctx(void)
{
    return secp256k1_context_create(SECP256K1_CONTEXT_SIGN | SECP256K1_CONTEXT_VERIFY);
}

/* Seed the store the way each tree's storage layer really writes: the
 * per-proof store where it exists, the legacy single blob otherwise. */
static bool seed_store(const std::vector<cashu::Proof> &proofs)
{
#ifdef HARNESS_PROOF_STORE
    return cashu::proof_store::save(0, proofs);
#else
    return seed_slot(0, "https://mint.example", proofs);
#endif
}

/* One payment's worth of change: spend the three lowest-indexed proofs, land
 * three fresh outputs. */
static std::vector<cashu::Proof> payment(const std::vector<cashu::Proof> &cur)
{
    std::vector<cashu::Proof> next;
    next.reserve(cur.size());
    for (size_t i = 3; i < cur.size(); i++) next.push_back(cur[i]);
    std::vector<cashu::Proof> change = synth_proofs(3, kProofId);
    for (auto &p : change) next.push_back(p);
    return next;
}

static std::vector<cashu::Proof> spend_three(const std::vector<cashu::Proof> &cur)
{
    return std::vector<cashu::Proof>(cur.begin() + 3, cur.end());
}

/* --- spend: the real wallet path, identical in both trees ------------- */
static int do_spend(int nproofs)
{
    secp256k1_context *ctx = make_ctx();
    if (!ctx) { printf("FATAL: secp256k1_context_create failed\n"); return 2; }
    if (nvs_flash_init() != ESP_OK) { printf("FATAL: nvs init\n"); return 2; }

    std::vector<cashu::Proof> base = synth_proofs(nproofs, kProofId);
    if (!seed_store(base)) { printf("FATAL: seeding the store failed\n"); return 2; }

    cashu::Wallet w("https://mint.example", ctx, 0);
    if (!w.load_from_nvs()) { printf("FATAL: wallet load failed\n"); return 2; }
    if (w.proofs().size() != (size_t)nproofs) {
        printf("FATAL: wallet loaded %zu of %d proofs\n", w.proofs().size(), nproofs);
        return 2;
    }
    std::vector<cashu::Proof> spent(w.proofs().begin(), w.proofs().begin() + 3);
    std::vector<cashu::Proof> expect = spend_three(w.proofs());

    nucula_nvs_reset_stats();
    nucula_nvs_reset_ops();
    int64_t t0 = esp_timer_get_time();
    bool ok = w.remove_proofs(spent);
    int64_t t1 = esp_timer_get_time();

    unsigned long long bytes = (unsigned long long)nucula_nvs_bytes_written();
    int wr = nucula_nvs_writes(), er = nucula_nvs_erases();

    cashu::Wallet v("https://mint.example", ctx, 0);
    bool reload_ok = v.load_from_nvs() && canon(v.proofs()) == canon(expect);

    printf("---- SPEND: real wallet path, Wallet::remove_proofs() ----\n");
    printf("proofs                  = %d -> %zu\n", nproofs, expect.size());
    printf("spend_ok                = %s\n", ok ? "true" : "false");
    printf("reload_matches          = %s\n", reload_ok ? "true" : "false");
    printf("nvs_bytes_mutation      = %llu\n", bytes);
    printf("nvs_write_ops           = %d\n", wr);
    printf("nvs_erase_ops           = %d\n", er);
    printf("spend_us                = %lld\n", (long long)(t1 - t0));
    printf("rss_kb                  = %ld\n", rss_kb());
    printf("threads                 = %ld\n", proc_threads());
    printf("SPEND_RESULT {\"proofs\":%d,\"proofs_after\":%zu,\"bytes_mutation\":%llu,"
           "\"write_ops\":%d,\"erase_ops\":%d,\"spend_us\":%lld,\"threads\":%ld,"
           "\"rss_kb\":%ld,\"spend_ok\":%s,\"reload_matches\":%s}\n",
           nproofs, expect.size(), bytes, wr, er, (long long)(t1 - t0),
           proc_threads(), rss_kb(), ok ? "true" : "false", reload_ok ? "true" : "false");
    fflush(stdout);

    secp256k1_context_destroy(ctx);
    return (ok && reload_ok) ? 0 : 1;
}

#ifdef HARNESS_PROOF_STORE
static bool store_roundtrip(const std::vector<cashu::Proof> &set)
{
    if (!cashu::proof_store::save(0, set)) return false;
    std::vector<cashu::Proof> got;
    if (!cashu::proof_store::load(0, got)) return false;
    return canon(got) == canon(set);
}

static int do_pay(int nproofs, int payments)
{
    secp256k1_context *ctx = make_ctx();
    if (!ctx) { printf("FATAL: secp256k1_context_create failed\n"); return 2; }
    if (nvs_flash_init() != ESP_OK) { printf("FATAL: nvs init\n"); return 2; }

    std::vector<cashu::Proof> cur = synth_proofs(nproofs, kProofId);

    nucula_nvs_reset_stats();
    nucula_nvs_reset_ops();
    if (!cashu::proof_store::save(0, cur)) { printf("FATAL: seed save failed\n"); return 2; }
    unsigned long long seed_bytes = (unsigned long long)nucula_nvs_bytes_written();
    int seed_wr = nucula_nvs_writes(), seed_er = nucula_nvs_erases();

    std::vector<cashu::Proof> back;
    bool seed_rt = cashu::proof_store::load(0, back) && canon(back) == canon(cur);

    printf("---- SEED: one write of the whole set ----\n");
    printf("nvs_bytes_seed          = %llu\n", seed_bytes);
    printf("nvs_seed_writes         = %d\n", seed_wr);
    printf("nvs_seed_erases         = %d\n", seed_er);
    printf("seed_roundtrip          = %s\n", seed_rt ? "true" : "false");

    printf("---- PAYMENTS: spend 3 inputs, land 3 change outputs ----\n");
    unsigned long long total = 0;
    int bad = 0;
    int64_t total_us = 0;
    int proof_slots = 0;
    for (int k = 0; k < payments; k++) {
        std::vector<cashu::Proof> next = payment(cur);
        nucula_nvs_reset_stats();
        nucula_nvs_reset_ops();
        int64_t t0 = esp_timer_get_time();
        bool ok = cashu::proof_store::save(0, next);
        int64_t t1 = esp_timer_get_time();
        unsigned long long b = (unsigned long long)nucula_nvs_bytes_written();
        int wr = nucula_nvs_writes(), er = nucula_nvs_erases();
        std::vector<cashu::Proof> got;
        bool rt = cashu::proof_store::load(0, got) && canon(got) == canon(next);
        if (!ok || !rt) bad++;
        total += b;
        total_us += (t1 - t0);
        printf("payment[%d]              proofs=%zu bytes=%llu writes=%d erases=%d "
               "us=%lld roundtrip=%s\n",
               k, next.size(), b, wr, er, (long long)(t1 - t0), rt ? "true" : "false");
        cur = std::move(next);
    }
    {
        nvs_handle_t h;
        uint16_t mark = 0;
        if (nvs_open("wallet", NVS_READONLY, &h) == ESP_OK) {
            nvs_get_u16(h, "pn_0", &mark);
            nvs_close(h);
        }
        proof_slots = (int)mark;
    }

    nucula_nvs_reset_stats();
    nucula_nvs_reset_ops();
    cashu::proof_store::save(0, cur);
    unsigned long long noop_bytes = (unsigned long long)nucula_nvs_bytes_written();
    int noop_writes = nucula_nvs_writes();

    printf("---- STEADY STATE ----\n");
    printf("live_proofs             = %zu\n", cur.size());
    printf("proof_slots             = %d\n", proof_slots);
    printf("unchanged_save_bytes    = %llu\n", noop_bytes);
    printf("unchanged_save_writes   = %d\n", noop_writes);
    printf("rss_kb                  = %ld\n", rss_kb());
    printf("threads                 = %ld\n", proc_threads());
    printf("PAY_RESULT {\"proofs\":%d,\"payments\":%d,\"bytes_seed\":%llu,"
           "\"bytes_payments_total\":%llu,\"payment_us_total\":%lld,"
           "\"live_proofs\":%zu,\"proof_slots\":%d,\"unchanged_save_bytes\":%llu,"
           "\"unchanged_save_writes\":%d,\"threads\":%ld,\"rss_kb\":%ld,"
           "\"roundtrip_failures\":%d,\"seed_roundtrip\":%s}\n",
           nproofs, payments, seed_bytes, total, (long long)total_us, cur.size(),
           proof_slots, noop_bytes, noop_writes, proc_threads(), rss_kb(), bad,
           seed_rt ? "true" : "false");
    fflush(stdout);

    secp256k1_context_destroy(ctx);
    return bad == 0 ? 0 : 1;
}

/* --- storetest -------------------------------------------------------- */

static int g_st_fail = 0;
static int g_st_checks = 0;

static void check(bool ok, const char *what, const char *detail = "")
{
    printf("  [%s] %s%s%s\n", ok ? "PASS" : "FAIL", what,
           detail[0] ? " -- " : "", detail);
    g_st_checks++;
    if (!ok) g_st_fail++;
}

static uint16_t proof_slot_mark(void)
{
    nvs_handle_t h;
    uint16_t mark = 0;
    if (nvs_open("wallet", NVS_READONLY, &h) != ESP_OK) return 0;
    nvs_get_u16(h, "pn_0", &mark);
    nvs_close(h);
    return mark;
}

static bool legacy_blob_present(void)
{
    nvs_handle_t h;
    if (nvs_open("wallet", NVS_READONLY, &h) != ESP_OK) return false;
    size_t len = 0;
    bool present = (nvs_get_blob(h, "proofs_0", nullptr, &len) == ESP_OK);
    nvs_close(h);
    return present;
}

static void write_legacy_blob(const std::vector<cashu::Proof> &set)
{
    nvs_handle_t h;
    if (nvs_open("wallet", NVS_READWRITE, &h) != ESP_OK) return;
    nvs_set_str(h, "url_0", "https://mint.example");
    std::string blob = cashu::proofs_to_json(set);
    nvs_set_blob(h, "proofs_0", blob.data(), blob.size());
    nvs_commit(h);
    nvs_close(h);
}

/* Every interruption point of one save: cut the save off after op k, for
 * every k from "nothing lands" to "everything lands", and assert the stored
 * set S only ever moves inside old∩new ⊆ S ⊆ old∪new.
 * mode 0 = per-proof store only, 1 = legacy blob only, 2 = both. */
static void sweep_save(const char *what, const std::vector<cashu::Proof> &old_set,
                       const std::vector<cashu::Proof> &new_set, int mode)
{
    const std::vector<std::string> Old = canon(old_set), New = canon(new_set);
    const std::vector<std::string> must = multiset_and(Old, New);
    const std::vector<std::string> may = multiset_or(Old, New);

    auto restore = [&]() {
        if (mode == 1) {
            nvs_flash_erase();
            write_legacy_blob(old_set);
        } else {
            cashu::proof_store::save(0, old_set);
            if (mode == 2) write_legacy_blob(old_set);
        }
    };

    restore();
    nucula_nvs_reset_ops();
    if (!cashu::proof_store::save(0, new_set)) {
        printf("  [FAIL] %s: the uninterrupted save itself failed\n", what);
        g_st_fail++;
        return;
    }
    const int ops = nucula_nvs_ops();   /* every mutating op the save attempted */

    int as_old = 0, as_new = 0, between = 0, losses = 0, phantoms = 0, failed = 0;
    for (int k = 0; k <= ops; k++) {
        restore();
        nucula_nvs_reset_ops();
        nucula_nvs_set_fail_after(k);
        bool ok = cashu::proof_store::save(0, new_set);
        nucula_nvs_set_fail_after(-1);
        if (!ok) failed++;

        std::vector<cashu::Proof> got;
        bool loaded = cashu::proof_store::load(0, got);
        std::vector<std::string> S = canon(got);
        if (!loaded || !multiset_in(must, S)) losses++;    // a proof vanished
        if (!loaded || !multiset_in(S, may)) phantoms++;   // an unknown proof
        if (S == Old) as_old++;
        else if (S == New) as_new++;
        else between++;
    }
    printf("  [%s] %s: %d interruption points -> %d left the old state, %d the "
           "new one, %d a superset/stale-proof state; proofs lost=%d, unknown "
           "proofs=%d, save reported failure=%d\n",
           (losses || phantoms) ? "FAIL" : "PASS", what, ops + 1, as_old, as_new,
           between, losses, phantoms, failed);
    if (losses || phantoms) g_st_fail++;
}

static int do_storetest(void)
{
    secp256k1_context *ctx = make_ctx();
    if (!ctx) { printf("FATAL: secp256k1_context_create failed\n"); return 2; }
    if (nvs_flash_init() != ESP_OK) { printf("FATAL: nvs init\n"); return 2; }

    std::vector<cashu::Proof> base = synth_proofs(200, kProofId);
    std::vector<cashu::Proof> one = synth_proofs(1, kProofId);
    std::vector<cashu::Proof> empty;
    std::vector<cashu::Proof> delta = payment(base);
    std::vector<cashu::Proof> smaller = spend_three(base);

    char d[220];

    printf("== round trips ==\n");
    check(store_roundtrip(empty), "empty set round-trips");
    check(store_roundtrip(one), "single proof round-trips");
    check(store_roundtrip(base), "200 proofs round-trip");
    check(store_roundtrip(delta), "200 proofs after a payment round-trip");
    check(store_roundtrip(smaller), "200 proofs after a spend round-trip");

    printf("== what a save writes (the wear claim) ==\n");
    cashu::proof_store::save(0, base);
    nucula_nvs_reset_stats();
    nucula_nvs_reset_ops();
    bool ok = cashu::proof_store::save(0, base);
    snprintf(d, sizeof d, "%llu bytes, %d write ops, %d erase ops",
             (unsigned long long)nucula_nvs_bytes_written(),
             nucula_nvs_writes(), nucula_nvs_erases());
    check(ok && nucula_nvs_bytes_written() == 0 && nucula_nvs_writes() == 0,
          "re-saving an unchanged set writes no proof bytes", d);

    cashu::proof_store::save(0, base);
    nucula_nvs_reset_stats();
    nucula_nvs_reset_ops();
    ok = cashu::proof_store::save(0, smaller);
    snprintf(d, sizeof d, "%llu bytes, %d write ops, %d erase ops",
             (unsigned long long)nucula_nvs_bytes_written(),
             nucula_nvs_writes(), nucula_nvs_erases());
    check(ok && nucula_nvs_bytes_written() == 0 && nucula_nvs_writes() == 0,
          "a spend of 3 proofs writes no proof bytes, only erases", d);

    // A payment into a full store: no holes to reuse, so its three outputs land
    // above the mark and the mark itself has to be written to publish them.
    // Three entries + the count, and the three spent slots are erased.
    const size_t entry_bytes = cashu::serialize(base[0]).size() + 1;   // + type byte
    const size_t count_bytes = 3;                                      // u16 + type byte
    cashu::proof_store::save(0, delta);
    std::vector<cashu::Proof> churn = payment(delta);
    nucula_nvs_reset_stats();
    nucula_nvs_reset_ops();
    ok = cashu::proof_store::save(0, churn);
    snprintf(d, sizeof d,
             "%llu bytes, %d write ops, %d erase ops for 3 change outputs (%zu B per entry)",
             (unsigned long long)nucula_nvs_bytes_written(),
             nucula_nvs_writes(), nucula_nvs_erases(), entry_bytes);
    check(ok && nucula_nvs_bytes_written() == 3 * entry_bytes + count_bytes &&
          nucula_nvs_writes() == 4 && nucula_nvs_erases() == 3,
          "a payment into a full store writes one entry per landed output plus the count", d);

    // Steady state: the save above erased the three slots it freed, so this
    // payment's three outputs land in those holes and the count does not move --
    // exactly three entries, whatever the wallet size.
    std::vector<cashu::Proof> churn2 = payment(churn);
    nucula_nvs_reset_stats();
    nucula_nvs_reset_ops();
    ok = cashu::proof_store::save(0, churn2);
    snprintf(d, sizeof d,
             "%llu bytes, %d write ops, %d erase ops for 3 change outputs (%zu B per entry)",
             (unsigned long long)nucula_nvs_bytes_written(),
             nucula_nvs_writes(), nucula_nvs_erases(), entry_bytes);
    check(ok && nucula_nvs_bytes_written() == 3 * entry_bytes &&
          nucula_nvs_writes() == 3 && nucula_nvs_erases() == 3,
          "a steady-state payment writes one entry per landed output, no count", d);

    printf("== slot allocation ==\n");
    {
        std::vector<cashu::Proof> cur = base;
        cashu::proof_store::save(0, cur);
        for (int i = 0; i < 40; i++) {
            cur = payment(cur);
            if (!cashu::proof_store::save(0, cur)) break;
        }
        unsigned mark = proof_slot_mark();
        snprintf(d, sizeof d, "mark=%u after 40 payments at %zu proofs", mark, cur.size());
        check(mark > 0 && mark <= cur.size() + 4,
              "the high-water mark does not grow with churn", d);
    }

    printf("== interruption sweeps (every save can stop after op k) ==\n");
    sweep_save("payment (per-proof store)", base, delta, 0);
    sweep_save("spend only (per-proof store)", base, smaller, 0);
    sweep_save("clear (per-proof store)", base, empty, 0);
    sweep_save("first migration (legacy blob present)", base, delta, 1);
    sweep_save("legacy blob still lingering", base, delta, 2);

    printf("== legacy compatibility and retirement ==\n");
    {
        nvs_flash_erase();
        write_legacy_blob(base);
        std::vector<cashu::Proof> got;
        bool loaded = cashu::proof_store::load(0, got);
        check(loaded && canon(got) == canon(base), "a legacy blob still loads");
        check(legacy_blob_present(), "the legacy blob is untouched by a load");
        bool saved = cashu::proof_store::save(0, delta);
        snprintf(d, sizeof d, "save=%s legacy_blob_present=%s", saved ? "true" : "false",
                 legacy_blob_present() ? "true" : "false");
        check(saved && !legacy_blob_present(), "a successful save retires the legacy blob", d);
        std::vector<cashu::Proof> after;
        check(cashu::proof_store::load(0, after) && canon(after) == canon(delta),
              "the migrated store reads back as the new set");
    }

    printf("== fault isolation and recovery ==\n");
    {
        cashu::proof_store::save(0, base);
        nvs_handle_t h;
        if (nvs_open("wallet", NVS_READWRITE, &h) == ESP_OK) {
            const char *junk = "{\"bad\":";
            nvs_set_blob(h, "p0_5", junk, strlen(junk));
            nvs_commit(h);
            nvs_close(h);
        }
        std::vector<cashu::Proof> got;
        bool loaded = cashu::proof_store::load(0, got);
        snprintf(d, sizeof d, "%zu of %zu proofs loaded from the damaged store",
                 got.size(), base.size());
        check(loaded && got.size() == base.size() - 1,
              "one damaged entry does not take the whole set down", d);
    }
    {
        cashu::proof_store::save(0, base);
        nucula_nvs_set_fail_after(3);
        bool failed = !cashu::proof_store::save(0, delta);
        nucula_nvs_set_fail_after(-1);
        check(failed, "an injected failure is reported by the save");
        bool ok2 = cashu::proof_store::save(0, delta);
        std::vector<cashu::Proof> got;
        check(ok2 && cashu::proof_store::load(0, got) && canon(got) == canon(delta),
              "a retry after a failed save converges on the exact set");
    }
    {
        cashu::proof_store::save(0, base);
        cashu::Wallet w("https://mint.example", ctx, 0);
        bool erased = w.erase_nvs();
        std::vector<cashu::Proof> got;
        snprintf(d, sizeof d, "erase_nvs=%s legacy_blob_present=%s",
                 erased ? "true" : "false", legacy_blob_present() ? "true" : "false");
        check(erased && !cashu::proof_store::load(0, got) && !legacy_blob_present(),
              "erase_nvs removes both stores", d);
    }

    printf("STORETEST_RESULT checks_done=%d failures=%d\n", g_st_checks, g_st_fail);
    fflush(stdout);
    secp256k1_context_destroy(ctx);
    return g_st_fail == 0 ? 0 : 1;
}
#endif  /* HARNESS_PROOF_STORE */

int main(int argc, char **argv)
{
    const char *mode = (argc > 1) ? argv[1] : "selftest";
    int nproofs = 200;
    int hold = 0;
    int payments = 5;
    for (int i = 2; i < argc; i++) {
        if (!strcmp(argv[i], "--proofs") && i + 1 < argc) nproofs = atoi(argv[++i]);
        else if (!strcmp(argv[i], "--hold") && i + 1 < argc) hold = atoi(argv[++i]);
        else if (!strcmp(argv[i], "--payments") && i + 1 < argc) payments = atoi(argv[++i]);
    }

    if (!strcmp(mode, "selftest")) return do_selftest();
    if (!strcmp(mode, "measure")) return do_measure(nproofs, hold);
    if (!strcmp(mode, "spend")) return do_spend(nproofs);
#ifdef HARNESS_PROOF_STORE
    if (!strcmp(mode, "pay")) return do_pay(nproofs, payments);
    if (!strcmp(mode, "storetest")) return do_storetest();
#endif

    fprintf(stderr, "usage: %s {selftest|measure|spend"
#ifdef HARNESS_PROOF_STORE
                    "|pay|storetest"
#endif
                    "} [--proofs N] [--hold S] [--payments N]\n", argv[0]);
    return 2;
}
