package wireless_gateway_manager

import (
	"fmt"
	"io"
	"net/http"
	"time"

	logrus "github.com/sirupsen/logrus"
)

type ProbeResult struct {
	GatewayIP     string        `json:"gateway_ip"`
	RTT           time.Duration `json:"rtt_ms"`
	ResponseBytes int           `json:"response_bytes"`
	Reachable     bool          `json:"reachable"`
	ProbedAt      time.Time     `json:"probed_at"`
	Error         string        `json:"error,omitempty"`
}

const ProbeTimeout = 5 * time.Second
const ProbePort = 2121

func SpeedProbe(gatewayIP string) ProbeResult {
	result := ProbeResult{
		GatewayIP: gatewayIP,
		ProbedAt:  time.Now(),
	}

	client := &http.Client{Timeout: ProbeTimeout}

	url := fmt.Sprintf("http://%s:%d/", gatewayIP, ProbePort)
	start := time.Now()

	resp, err := client.Get(url)
	result.RTT = time.Since(start)

	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		result.Error = err.Error()
		return result
	}

	result.ResponseBytes = len(body)
	result.Reachable = resp.StatusCode == 200

	logger.WithFields(logrus.Fields{
		"gateway": gatewayIP,
		"rtt_ms":  result.RTT.Milliseconds(),
		"bytes":   result.ResponseBytes,
		"status":  resp.StatusCode,
	}).Debug("Speed probe completed")

	return result
}
