#!/bin/sh
# inventory.sh — machine-generated ESP-IDF API-boundary inventory for nucula.
#
# Part of the nucula OpenWrt port spike. OUR script.
#
# Reads an unpacked nucula tree and, for every first-party source in main/,
# emits the ESP-IDF headers it includes and the ESP-IDF / third-party symbols
# it references. The output is the raw evidence behind
# research/wallet-migration/01-candidates/nucula-port-map.md.
#
# It does NOT classify files as core/platform/peripheral — that is a judgement
# call recorded in the port map — it only reports facts about the source.
#
# Usage: ./inventory.sh /path/to/nucula/src > raw/inventory.txt
set -eu

SRC="${1:?usage: inventory.sh /path/to/nucula/src}"
MAIN="$SRC/main"
test -d "$MAIN" || { echo "no $MAIN" >&2; exit 1; }

echo "nucula source: $MAIN"
echo "generated-by : experiments/nucula-port/inventory.sh"
echo

echo "## 1. ESP-IDF headers included, per file"
echo
printf '%-26s %6s  %s\n' FILE LINES "ESP-IDF HEADERS"
for f in "$MAIN"/*.c "$MAIN"/*.cpp "$MAIN"/*.h "$MAIN"/*.hpp; do
    [ -f "$f" ] || continue
    b=$(basename "$f")
    lines=$(wc -l < "$f")
    hdrs=$(grep -hoE '#include[[:space:]]*[<"](esp_[a-z_]+|nvs[a-z_]*|nvs_flash|freertos/[A-Za-z]+|driver/[a-z_]+|esp_wifi|esp_netif|esp_event|esp_http_client|esp_timer|esp_random|esp_heap_caps|esp_crt_bundle|esp_system|esp_err|esp_log|usb_serial_jtag[a-z_]*|hal/[a-z_]+)[.a-z]*[>"]' "$f" 2>/dev/null \
        | sed -E 's/#include[[:space:]]*[<"]([^>"]*)[>"]/\1/' | sort -u | tr '\n' ' ')
    printf '%-26s %6s  %s\n' "$b" "$lines" "$hdrs"
done

echo
echo "## 2. ESP-IDF / platform symbol references, per file"
echo
printf '%-26s %s\n' FILE "SYMBOLS (count)"
for f in "$MAIN"/*.c "$MAIN"/*.cpp; do
    [ -f "$f" ] || continue
    b=$(basename "$f")
    syms=$(grep -hoE '\b(esp_[a-z0-9_]+|nvs_[a-z0-9_]+|ESP_[A-Z0-9_]+|xTask[A-Za-z]+|vTask[A-Za-z]+|xSemaphore[A-Za-z]+|vSemaphore[A-Za-z]+|xQueue[A-Za-z]+|pdMS_TO_TICKS|portMAX_DELAY|heap_caps_[a-z_]+|esp_netif_[a-z_]+|gpio_[a-z_]+|i2c_[a-z_]+|usb_serial_jtag_[a-z_]+|mbedtls_[a-z0-9_]+|esp_timer_[a-z_]+|MALLOC_CAP_[A-Z_]+)\b' "$f" 2>/dev/null \
        | sort | uniq -c | sort -rn | awk '{printf "%s(%s) ", $2, $1}')
    printf '%-26s %s\n' "$b" "$syms"
done

echo
echo "## 3. Third-party (non-IDF) library headers, per file"
echo
printf '%-26s %s\n' FILE HEADERS
for f in "$MAIN"/*.c "$MAIN"/*.cpp; do
    [ -f "$f" ] || continue
    b=$(basename "$f")
    hdrs=$(grep -hoE '#include[[:space:]]*[<"](mbedtls/[a-z0-9_]+\.h|cJSON\.h|cbor\.h|secp256k1[a-z_]*\.h)[>"]' "$f" 2>/dev/null \
        | sed -E 's/#include[[:space:]]*[<"]([^>"]*)[>"]/\1/' | sort -u | tr '\n' ' ')
    printf '%-26s %s\n' "$b" "$hdrs"
done

echo
echo "## 4. Totals"
echo
t=$(cat "$MAIN"/*.c "$MAIN"/*.cpp 2>/dev/null | wc -l)
echo "main/ .c/.cpp total lines: $t"
idf=$(grep -hoE '\b(esp_[a-z0-9_]+|nvs_[a-z0-9_]+|ESP_[A-Z0-9_]+|xTask[A-Za-z]+|vTask[A-Za-z]+|xSemaphore[A-Za-z]+|vSemaphore[A-Za-z]+|xQueue[A-Za-z]+|pdMS_TO_TICKS|portMAX_DELAY)\b' "$MAIN"/*.c "$MAIN"/*.cpp 2>/dev/null | wc -l)
echo "ESP-IDF symbol references in main/*.c/*.cpp: $idf"
echo "files that include an ESP-IDF header: $(grep -lE '#include[[:space:]]*[<"](esp_|nvs|freertos/|driver/)' "$MAIN"/*.c "$MAIN"/*.cpp "$MAIN"/*.h "$MAIN"/*.hpp 2>/dev/null | wc -l)"
echo "files that include NO ESP-IDF header: $(for f in "$MAIN"/*.c "$MAIN"/*.cpp "$MAIN"/*.h "$MAIN"/*.hpp; do [ -f "$f" ] || continue; grep -qE '#include[[:space:]]*[<"](esp_|nvs|freertos/|driver/)' "$f" || echo "$f"; done | wc -l)"
