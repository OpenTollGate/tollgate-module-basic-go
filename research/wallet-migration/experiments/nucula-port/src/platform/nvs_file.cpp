/*
 * nvs_file.cpp — file-backed implementation of the ESP-IDF NVS subset.
 *
 * Part of the nucula OpenWrt port spike. OUR code (a replacement, not a port
 * of ESP-IDF's nvs_partition / spi_flash_emulation).
 *
 * Layout on disk:
 *   $NUCULA_NVS_DIR/<namespace>/<hex-encoded-key>   (one file per key)
 *   file bytes: [0]=type  [1..]=payload
 *     type 1 = string (payload includes the trailing NUL, as ESP-IDF stores it)
 *     type 2 = blob   (payload is raw bytes)
 *     type 3 = u8     4 = u16     5 = u32     6 = i32
 *
 * Durability: strings and blobs are written to a temporary file in the same
 * directory, fsync'd, then rename(2)'d over the target. rename(2) is atomic on
 * JFFS2/UBIFS/ext4 for same-directory renames, which is what gives the port
 * its per-key crash consistency — a DIFFERENT guarantee from ESP-IDF NVS's
 * whole-page copy-on-write, and one the port verdict calls out.
 *
 * What this shim deliberately does NOT do (each is a port risk, not a bug):
 *   - no wear levelling and no erase-cycle accounting (the filesystem's job);
 *   - no partition size limit, so nucula's "NVS full" failure mode is
 *     unreachable here and therefore untested;
 *   - no encryption-at-rest (ESP-IDF NVS encryption is off in nucula anyway,
 *     so behaviour is equal — the seed lands in plaintext in both).
 */
#include "nvs.h"
#include "esp_log.h"

#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

#define TAG "nvs-shim"

enum { T_STR = 1, T_BLOB = 2, T_U8 = 3, T_U16 = 4, T_U32 = 5, T_I32 = 6 };

#define MAX_OPEN 32

struct nvs_handle_slot {
    int         used;
    int         readonly;
    char        ns[64];
};
static struct nvs_handle_slot g_slots[MAX_OPEN];
static pthread_mutex_t g_lock = PTHREAD_MUTEX_INITIALIZER;
static int g_inited = 0;
static uint64_t g_bytes_written = 0;
static char g_dir[512] = {0};

static const char *store_dir(void)
{
    if (g_dir[0]) return g_dir;
    const char *env = getenv("NUCULA_NVS_DIR");
    if (env && *env) {
        snprintf(g_dir, sizeof g_dir, "%s", env);
    } else {
        snprintf(g_dir, sizeof g_dir, "./nucula-nvs");
    }
    return g_dir;
}

const char *nucula_nvs_dir(void) { return store_dir(); }

uint64_t nucula_nvs_bytes_written(void) { return g_bytes_written; }
void nucula_nvs_reset_stats(void) { g_bytes_written = 0; }

static void mkdir_p(const char *path)
{
    char tmp[640];
    snprintf(tmp, sizeof tmp, "%s", path);
    for (char *p = tmp + 1; *p; p++) {
        if (*p == '/') {
            *p = '\0';
            mkdir(tmp, 0700);
            *p = '/';
        }
    }
    mkdir(tmp, 0700);
}

/* Hex-encode the key so any printable key the wallet uses is a safe filename
 * (ESP-IDF restricts keys to 15 chars and forbids '/'; the shim is stricter
 * than it needs to be so it cannot collide). */
static void key_path(const char *ns, const char *key, char *out, size_t n)
{
    char hex[256];
    size_t i = 0;
    for (; key[i] && i < 120; i++)
        snprintf(hex + i * 2, 3, "%02x", (unsigned char)key[i]);
    hex[i * 2] = '\0';
    snprintf(out, n, "%s/%s/%s", store_dir(), ns, hex);
}

esp_err_t nvs_flash_init(void)
{
    pthread_mutex_lock(&g_lock);
    char base[640];
    snprintf(base, sizeof base, "%s", store_dir());
    mkdir_p(base);
    struct stat st;
    if (stat(base, &st) != 0 || !S_ISDIR(st.st_mode)) {
        pthread_mutex_unlock(&g_lock);
        ESP_LOGE(TAG, "cannot create NVS dir %s", base);
        return ESP_ERR_NVS_NOT_FOUND;
    }
    g_inited = 1;
    pthread_mutex_unlock(&g_lock);
    return ESP_OK;
}

esp_err_t nvs_flash_erase(void)
{
    /* ESP-IDF wipes the partition. The shim removes the whole directory tree
     * one level deep, which is all the wallet's namespaces ever occupy. */
    char base[640], cmd[700];
    snprintf(base, sizeof base, "%s", store_dir());
    DIR *d = opendir(base);
    if (!d) return ESP_ERR_NVS_NOT_FOUND;
    struct dirent *e;
    while ((e = readdir(d))) {
        if (!strcmp(e->d_name, ".") || !strcmp(e->d_name, "..")) continue;
        char sub[900];
        snprintf(sub, sizeof sub, "%s/%s", base, e->d_name);
        DIR *sd = opendir(sub);
        if (sd) {
            struct dirent *se;
            while ((se = readdir(sd))) {
                if (!strcmp(se->d_name, ".") || !strcmp(se->d_name, "..")) continue;
                snprintf(cmd, sizeof cmd, "%s/%s", sub, se->d_name);
                unlink(cmd);
            }
            closedir(sd);
        }
        rmdir(sub);
    }
    closedir(d);
    return ESP_OK;
}

esp_err_t nvs_flash_deinit(void)
{
    g_inited = 0;
    return ESP_OK;
}

esp_err_t nvs_open(const char *namespace_name, nvs_open_mode_t open_mode,
                   nvs_handle_t *out_handle)
{
    if (!out_handle || !namespace_name) return ESP_ERR_INVALID_ARG;
    if (!g_inited) {
        esp_err_t e = nvs_flash_init();
        if (e != ESP_OK) return e;
    }

    char dir[640];
    snprintf(dir, sizeof dir, "%s/%s", store_dir(), namespace_name);
    struct stat st;
    int exists = (stat(dir, &st) == 0 && S_ISDIR(st.st_mode));

    if (!exists) {
        if (open_mode == NVS_READONLY)
            return ESP_ERR_NVS_NOT_FOUND;
        mkdir_p(dir);
    }

    pthread_mutex_lock(&g_lock);
    for (int i = 0; i < MAX_OPEN; i++) {
        if (!g_slots[i].used) {
            g_slots[i].used = 1;
            g_slots[i].readonly = (open_mode == NVS_READONLY);
            snprintf(g_slots[i].ns, sizeof g_slots[i].ns, "%s", namespace_name);
            /* Handle 0 is reserved as "invalid", like a NULL handle. */
            *out_handle = (nvs_handle_t)(i + 1);
            pthread_mutex_unlock(&g_lock);
            return ESP_OK;
        }
    }
    pthread_mutex_unlock(&g_lock);
    return ESP_ERR_NO_MEM;
}

static struct nvs_handle_slot *slot_of(nvs_handle_t h)
{
    if (h == 0 || h > MAX_OPEN) return NULL;
    struct nvs_handle_slot *s = &g_slots[h - 1];
    return s->used ? s : NULL;
}

void nvs_close(nvs_handle_t handle)
{
    pthread_mutex_lock(&g_lock);
    struct nvs_handle_slot *s = slot_of(handle);
    if (s) { s->used = 0; s->ns[0] = '\0'; }
    pthread_mutex_unlock(&g_lock);
}

esp_err_t nvs_commit(nvs_handle_t handle)
{
    /* Write-through + fsync on every set, so nothing is pending. */
    return slot_of(handle) ? ESP_OK : ESP_ERR_NVS_INVALID_HANDLE;
}

static esp_err_t read_entry(const char *ns, const char *key,
                            unsigned char **payload, size_t *plen, int expect_type)
{
    char path[900];
    key_path(ns, key, path, sizeof path);
    int fd = open(path, O_RDONLY);
    if (fd < 0) return ESP_ERR_NVS_NOT_FOUND;

    struct stat st;
    if (fstat(fd, &st) != 0 || st.st_size < 1) { close(fd); return ESP_ERR_NVS_NOT_FOUND; }
    size_t n = (size_t)st.st_size - 1;
    unsigned char *buf = (unsigned char *)malloc(n ? n : 1);
    if (!buf) { close(fd); return ESP_ERR_NO_MEM; }
    unsigned char type = 0;
    if (read(fd, &type, 1) != 1 || (n && read(fd, buf, n) != (ssize_t)n)) {
        free(buf); close(fd); return ESP_ERR_NVS_INVALID_STATE;
    }
    close(fd);
    if (expect_type >= 0 && type != expect_type) { free(buf); return ESP_ERR_NVS_TYPE_MISMATCH; }
    *payload = buf;
    *plen = n;
    return ESP_OK;
}

static esp_err_t write_entry(const char *ns, const char *key, unsigned char type,
                             const void *data, size_t len)
{
    char path[900], tmp[920];
    key_path(ns, key, path, sizeof path);
    snprintf(tmp, sizeof tmp, "%s.tmp", path);

    int fd = open(tmp, O_WRONLY | O_CREAT | O_TRUNC, 0600);
    if (fd < 0) {
        ESP_LOGE(TAG, "open %s: %s", tmp, strerror(errno));
        return ESP_ERR_NO_MEM;
    }
    unsigned char t = type;
    ssize_t w = write(fd, &t, 1);
    if (w == 1 && len > 0) w += write(fd, data, len);
    if (fsync(fd) != 0) { /* best effort; UBIFS/JFFS2 accept it */ }
    close(fd);

    if (w != (ssize_t)(len + 1)) { unlink(tmp); return ESP_ERR_NO_MEM; }
    if (rename(tmp, path) != 0) { unlink(tmp); return ESP_ERR_NVS_NOT_ENOUGH_SPACE; }

    g_bytes_written += len + 1;
    return ESP_OK;
}

esp_err_t nvs_erase_key(nvs_handle_t handle, const char *key)
{
    struct nvs_handle_slot *s = slot_of(handle);
    if (!s) return ESP_ERR_NVS_INVALID_HANDLE;
    if (s->readonly) return ESP_ERR_NVS_READ_ONLY;
    if (!key) return ESP_ERR_INVALID_ARG;
    char path[900];
    key_path(s->ns, key, path, sizeof path);
    return unlink(path) == 0 ? ESP_OK : ESP_ERR_NVS_NOT_FOUND;
}

esp_err_t nvs_erase_all(nvs_handle_t handle)
{
    struct nvs_handle_slot *s = slot_of(handle);
    if (!s) return ESP_ERR_NVS_INVALID_HANDLE;
    if (s->readonly) return ESP_ERR_NVS_READ_ONLY;
    char dir[700];
    snprintf(dir, sizeof dir, "%s/%s", store_dir(), s->ns);
    DIR *d = opendir(dir);
    if (!d) return ESP_ERR_NVS_NOT_FOUND;
    struct dirent *e;
    while ((e = readdir(d))) {
        if (!strcmp(e->d_name, ".") || !strcmp(e->d_name, "..")) continue;
        char p[1000];
        snprintf(p, sizeof p, "%s/%s", dir, e->d_name);
        unlink(p);
    }
    closedir(d);
    return ESP_OK;
}

esp_err_t nvs_get_str(nvs_handle_t handle, const char *key, char *out_value,
                      size_t *length)
{
    struct nvs_handle_slot *s = slot_of(handle);
    if (!s) return ESP_ERR_NVS_INVALID_HANDLE;
    if (!key || !length) return ESP_ERR_INVALID_ARG;

    unsigned char *payload = NULL;
    size_t plen = 0;
    esp_err_t e = read_entry(s->ns, key, &payload, &plen, T_STR);
    if (e != ESP_OK) return e;

    if (!out_value) { *length = plen; free(payload); return ESP_OK; }
    if (*length < plen) { *length = plen; free(payload); return ESP_ERR_NVS_INVALID_LENGTH; }
    memcpy(out_value, payload, plen);
    *length = plen;
    free(payload);
    return ESP_OK;
}

esp_err_t nvs_set_str(nvs_handle_t handle, const char *key, const char *value)
{
    struct nvs_handle_slot *s = slot_of(handle);
    if (!s || !key || !value) return ESP_ERR_INVALID_ARG;
    if (s->readonly) return ESP_ERR_NVS_READ_ONLY;
    return write_entry(s->ns, key, T_STR, value, strlen(value) + 1);
}

esp_err_t nvs_get_blob(nvs_handle_t handle, const char *key, void *out_value,
                       size_t *length)
{
    struct nvs_handle_slot *s = slot_of(handle);
    if (!s) return ESP_ERR_NVS_INVALID_HANDLE;
    if (!key || !length) return ESP_ERR_INVALID_ARG;

    unsigned char *payload = NULL;
    size_t plen = 0;
    esp_err_t e = read_entry(s->ns, key, &payload, &plen, T_BLOB);
    if (e != ESP_OK) return e;

    if (!out_value) { *length = plen; free(payload); return ESP_OK; }
    if (*length < plen) { *length = plen; free(payload); return ESP_ERR_NVS_INVALID_LENGTH; }
    memcpy(out_value, payload, plen);
    *length = plen;
    free(payload);
    return ESP_OK;
}

esp_err_t nvs_set_blob(nvs_handle_t handle, const char *key, const void *value,
                       size_t length)
{
    struct nvs_handle_slot *s = slot_of(handle);
    if (!s || !key || (!value && length)) return ESP_ERR_INVALID_ARG;
    if (s->readonly) return ESP_ERR_NVS_READ_ONLY;
    return write_entry(s->ns, key, T_BLOB, value, length);
}

#define NVS_INT_IMPL(TYPE, CTYPE, TAGCODE)                                     \
    esp_err_t nvs_get_##TYPE(nvs_handle_t handle, const char *key, CTYPE *out) \
    {                                                                          \
        struct nvs_handle_slot *s = slot_of(handle);                           \
        if (!s || !key || !out) return ESP_ERR_INVALID_ARG;                    \
        unsigned char *p = NULL; size_t n = 0;                                 \
        esp_err_t e = read_entry(s->ns, key, &p, &n, TAGCODE);                 \
        if (e != ESP_OK) return e;                                             \
        if (n != sizeof(CTYPE)) { free(p); return ESP_ERR_NVS_INVALID_LENGTH; }\
        memcpy(out, p, sizeof(CTYPE));                                         \
        free(p);                                                               \
        return ESP_OK;                                                         \
    }                                                                          \
    esp_err_t nvs_set_##TYPE(nvs_handle_t handle, const char *key, CTYPE v)    \
    {                                                                          \
        struct nvs_handle_slot *s = slot_of(handle);                           \
        if (!s || !key) return ESP_ERR_INVALID_ARG;                            \
        if (s->readonly) return ESP_ERR_NVS_READ_ONLY;                         \
        return write_entry(s->ns, key, TAGCODE, &v, sizeof v);                 \
    }

NVS_INT_IMPL(u8,  uint8_t,  T_U8)
NVS_INT_IMPL(u16, uint16_t, T_U16)
NVS_INT_IMPL(u32, uint32_t, T_U32)
NVS_INT_IMPL(i32, int32_t,  T_I32)
