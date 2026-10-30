package main

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/wan_health"
)

const defaultWANHealthPath = "/etc/tollgate/wan-health.json"

func wanHealthPath() string {
	if path := os.Getenv("TOLLGATE_WAN_HEALTH_PATH"); path != "" {
		return path
	}
	return defaultWANHealthPath
}

// handleWANHealth exposes the upstream Wi-Fi health snapshot and the portal
// toast. It is deliberately not gated by requireStarted: the snapshot is
// useful while the wallet/mint is still loading, and it contains no money or
// secret data.
func handleWANHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeWANHealthJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"success":   false,
			"error":     "method not allowed",
			"timestamp": time.Now().UTC(),
		})
		return
	}

	status, err := wan_health.Load(wanHealthPath())
	if err != nil {
		if os.IsNotExist(err) {
			writeWANHealthJSON(w, http.StatusOK, map[string]any{
				"success":   true,
				"message":   "no wan-health data yet",
				"data":      nil,
				"toast":     nil,
				"timestamp": time.Now().UTC(),
			})
			return
		}
		writeWANHealthJSON(w, http.StatusInternalServerError, map[string]any{
			"success":   false,
			"error":     err.Error(),
			"timestamp": time.Now().UTC(),
		})
		return
	}

	var toast any
	if t := status.Toast(); t.Active {
		toast = t
	}
	writeWANHealthJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"message":   "wan health",
		"data":      status,
		"toast":     toast,
		"timestamp": time.Now().UTC(),
	})
}

func writeWANHealthJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
