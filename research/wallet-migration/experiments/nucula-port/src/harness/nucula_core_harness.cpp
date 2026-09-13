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
#include "nvs.h"
#include "esp_log.h"
#include "esp_random.h"
#include "esp_timer.h"
#include "secp256k1.h"

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <string>
#include <vector>
#include <unistd.h>

#define TAG "harness"

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

int main(int argc, char **argv)
{
    const char *mode = (argc > 1) ? argv[1] : "selftest";
    int nproofs = 200;
    int hold = 0;
    for (int i = 2; i < argc; i++) {
        if (!strcmp(argv[i], "--proofs") && i + 1 < argc) nproofs = atoi(argv[++i]);
        else if (!strcmp(argv[i], "--hold") && i + 1 < argc) hold = atoi(argv[++i]);
    }

    if (!strcmp(mode, "selftest")) return do_selftest();
    if (!strcmp(mode, "measure")) return do_measure(nproofs, hold);

    fprintf(stderr, "usage: %s {selftest|measure} [--proofs N] [--hold S]\n", argv[0]);
    return 2;
}
