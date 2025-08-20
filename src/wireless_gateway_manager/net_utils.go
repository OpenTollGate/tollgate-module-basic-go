// Package wireless_gateway_manager provides utility functions for network operations.
package wireless_gateway_manager

import (
	"bufio"
	"bytes"
	"errors"
	"os/exec"
	"regexp"
	"strings"
)

// getSTAManagedInterfaces finds all network interfaces of type 'managed' (STA).
// It returns a map of the interface name to its corresponding radio device (e.g., "phy0-sta0" -> "radio0").
func getSTAManagedInterfaces() (map[string]string, error) {
	cmd := exec.Command("iw", "dev")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	interfaces := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(stdout.Bytes()))
	var currentPhy, currentInterface string

	phyRegex := regexp.MustCompile(`phy#(\d+)`)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "phy#") {
			matches := phyRegex.FindStringSubmatch(line)
			if len(matches) > 1 {
				currentPhy = "radio" + matches[1]
			}
		} else if strings.HasPrefix(line, "Interface") {
			parts := strings.Fields(line)
			if len(parts) > 1 {
				currentInterface = parts[1]
			}
		} else if strings.HasPrefix(line, "type") && strings.Contains(line, "managed") {
			if currentInterface != "" && currentPhy != "" {
				interfaces[currentInterface] = currentPhy
				// Reset for next interface block
				currentInterface = ""
				// currentPhy is sticky until the next phy is found
			}
		}
	}

	if len(interfaces) == 0 {
		return nil, errors.New("no managed Wi-Fi interfaces found")
	}
	return interfaces, nil
}