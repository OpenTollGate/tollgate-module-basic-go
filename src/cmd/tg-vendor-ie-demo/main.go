// Command tg-vendor-ie-demo proves the vendor-IE price producer against LIVE
// router output.
//
// It is deliberately NOT a unit test: unit tests use synthetic scan text. This
// runs the real producer on whatever the router's own tools emit, so the
// parsing claim is verified against the machine rather than against a fixture.
//
// Usage: tg-vendor-ie-demo <wireless-device>   e.g. tg-vendor-ie-demo phy0-ap0
//
// It shells out to `iw dev <dev> scan` (the only tool that prints information
// elements), feeds stdout through the same ParseIwinfoOutput the daemon uses,
// and prints every network with its identification and pricing fields.
//
// Exit codes: 0 = ran and at least one vendor element was parsed,
//
//	2 = ran but found no vendor element (reported, not a failure of
//	    the parser), 1 = could not run the scan at all.
package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/wireless_gateway_manager"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: tg-vendor-ie-demo <wireless-device> | --file <scan-file>")
		os.Exit(1)
	}
	dev := os.Args[1]

	fmt.Printf("=== tg-vendor-ie-demo on %s ===\n", dev)

	// Two modes:
	//   <device>        run `iw dev <device> scan` — proves the parser against
	//                   live air.
	//   --file <path>   parse a saved scan — lets a synthetic element be
	//                   injected into REAL captured output, so the positive
	//                   identification path can be proven where no neighbour
	//                   running this build exists.
	var iwOut []byte
	if dev == "--file" {
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: tg-vendor-ie-demo --file <scan-file>")
			os.Exit(1)
		}
		b, err := os.ReadFile(os.Args[2])
		if err != nil {
			fmt.Fprintf(os.Stderr, "read %s: %v\n", os.Args[2], err)
			os.Exit(1)
		}
		iwOut = b
		fmt.Printf("read %s: %d bytes\n", os.Args[2], len(iwOut))
	} else {
		out, iwErr := exec.Command("iw", "dev", dev, "scan").Output()
		iwOut = out
		fmt.Printf("iw dev %s scan: %d bytes, err=%v\n", dev, len(iwOut), iwErr)
	}

	if len(iwOut) == 0 {
		fmt.Println("RESULT: no iw scan output — cannot demonstrate parsing")
		os.Exit(1)
	}

	// Count the vendor elements present, independently of the parser.
	vendorLines := 0
	for _, line := range splitLines(string(iwOut)) {
		if len(line) >= 16 && line[0:16] == "Vendor specific:" {
			vendorLines++
		}
	}
	fmt.Printf("Vendor specific: lines present in raw output = %d\n", vendorLines)

	scanner := wireless_gateway_manager.NewScanner(nil, true)
	nets := scanner.ParseIwinfoOutput(iwOut, "demo")

	fmt.Printf("parsed networks = %d\n\n", len(nets))
	tollGates := 0
	for _, n := range nets {
		if !n.IsTollGate {
			continue
		}
		tollGates++
		fmt.Printf("TOLLGATE  bssid=%s ssid=%q signal=%d price_per_step=%d step_size=%d rawIE_bytes=%d\n",
			n.BSSID, n.SSID, n.Signal, n.PricePerStep, n.StepSize, len(n.RawIEs))
	}
	fmt.Printf("\nidentified TollGates = %d\n", tollGates)

	if tollGates == 0 {
		fmt.Println("RESULT: parser ran; no TollGate vendor element in range.")
		fmt.Println("        (Absence is the expected result unless another TollGate")
		fmt.Println("        running this build is also on the air.)")
		os.Exit(2)
	}
	fmt.Println("RESULT: vendor-IE producer populated identification + pricing from live output.")
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			// Trim leading whitespace so the "Vendor specific:" marker is at 0.
			for len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
				line = line[1:]
			}
			out = append(out, line)
			start = i + 1
		}
	}
	return out
}
