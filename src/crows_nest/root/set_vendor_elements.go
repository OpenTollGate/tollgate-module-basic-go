package main

import (
	"flag"
	"errors"
	"fmt"
	"github.com/digineo/go-uci"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"runtime/debug"
)

var (
	Version    string
	CommitHash string
	BuildTime  string
)

func stringToHex(s string) string {
	hexStr := ""
	for _, b := range []byte(s) {
		hexStr += fmt.Sprintf("%02x", b)
	}
	return hexStr
}

const oiu = "212121"     // TollGate custom OIU
const elementType = "01" // TollGate custom elementType
func createVendorElement(payload string) (string, error) {

	if len(payload) > 247 {
		return "", errors.New("payload cannot exceed 247 characters to stay within vendor_elements max size of 256 chars")
	}

	var vendorElementPayload = oiu + elementType + payload

	payloadLengthInBytesHex := strconv.FormatInt(int64(len(vendorElementPayload)), 16)
	payloadHex := stringToHex(vendorElementPayload)

	return "dd" + payloadLengthInBytesHex + payloadHex, nil
}

func setupBroadcast() {
	interfaces := []string{"default_radio0", "default_radio1"} // TODO: from config

	// TODO from config:		Pubkey of: merchant nsec17jlyx05kfqpyhrfuu6450x8shzlaslpngjnr8fe27raacmp49tzsvfaz9v
	var tollgateVersion = "v0.1.0"
	var gatewayIp = "192.168.1.1"
	var pubkey = "c1f4c025e746fd307203ac3d1a1886e343bea76ceec5e286c96fb353be6cadea"
	var allocationType = "KiB" // or min
	var priceAllocationPer1024 = "1049000"
	var priceUnit = "sat"
	var ssid = "TollGate - MaryGreen"

	salesPitch := []string{tollgateVersion, pubkey, allocationType, priceAllocationPer1024, priceUnit, gatewayIp}
	var salesPitchString = strings.Join(salesPitch, "|")

	log.Println(salesPitchString)
	vendorElement, err := createVendorElement(salesPitchString)

	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}

	var errEnableWifi = configureWirelessOption([]string{"radio0", "radio1"}, "disabled", "0")

	var errVendorElements = configureWirelessOption(interfaces, "vendor_elements", vendorElement)
	var errSSID = configureWirelessOption(interfaces, "ssid", ssid)
	if errVendorElements != nil {
		log.Fatal(errVendorElements)
	}

	if errSSID != nil {
		log.Fatal(errSSID)
	}

	if errEnableWifi != nil {
		log.Fatal(errEnableWifi)
	}

	reloadWifi()
}

func reloadWifi() {
	log.Println("reloading wifi")

	cmd := exec.Command("wifi", "reload")
	_, err := cmd.Output()
	if err != nil {
		log.Fatal(err, "Error reloading wifi")
	} else {
		log.Println("wifi reloaded")
	}
}

func getVersionInfo() string {
    if info, ok := debug.ReadBuildInfo(); ok {
        for _, setting := range info.Settings {
            switch setting.Key {
            case "vcs.revision":
                CommitHash = setting.Value[:7]
            case "vcs.time":
                BuildTime = setting.Value
            }
        }
    }
    return fmt.Sprintf("Version: %s\nCommit: %s\nBuild Time: %s", 
        Version, CommitHash, BuildTime)
}

func main() {
	// Add a version flag
	versionFlag := flag.Bool("version", false, "Print version information")
	flag.Parse()

	if *versionFlag {
		fmt.Println(getVersionInfo())
		return
	}

	log.Println("Starting Tollgate - CrowsNest")

	setupBroadcast()

	log.Println("Shutting down Tollgate - CrowsNest")
}

func configureWirelessOption(interfaces []string, name string, value string) error {
	for _, iface := range interfaces {
		log.Println("setting wireless interface " + iface + " option " + name + " to " + value)

		u := uci.NewTree("/etc/config")

		if ok := u.Set("wireless", iface, name, value); !ok {
			return errors.New("could not set option " + name + " on interface " + iface + ": " + value)
		}

		// Save edits
		err := u.Commit()
		if err != nil {
			log.Fatal(err)
			return err
		}
	}

	return nil
}
