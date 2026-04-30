package wireless_gateway_manager

import (
	"context"
	"fmt"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
)

func Init(ctx context.Context, configManager *config_manager.ConfigManager) (*GatewayManager, error) {
	connector := &Connector{}
	scanner := &Scanner{Connector: connector}
	vendorProcessor := &VendorElementProcessor{connector: connector}
	networkMonitor := NewNetworkMonitor(connector)

	gatewayManager := &GatewayManager{
		scanner:         scanner,
		connector:       connector,
		vendorProcessor: vendorProcessor,
		networkMonitor:  networkMonitor,
		configManager:   configManager,
		stopChan:        make(chan struct{}),
	}

	gatewayManager.networkMonitor.Start()

	return gatewayManager, nil
}

func (gm *GatewayManager) ScanAllRadios() ([]NetworkInfo, error) {
	return gm.scanner.ScanAllRadios()
}

func (gm *GatewayManager) FindBestRadioForSSID(ssid string, networks []NetworkInfo) (string, error) {
	return gm.scanner.FindBestRadioForSSID(ssid, networks)
}

func (gm *GatewayManager) DetectEncryption(encryptionStr string) string {
	return gm.scanner.DetectEncryption(encryptionStr)
}

func (gm *GatewayManager) GetSTASections() ([]STASection, error) {
	return gm.connector.GetSTASections()
}

func (gm *GatewayManager) GetActiveSTA() (*STASection, error) {
	return gm.connector.GetActiveSTA()
}

func (gm *GatewayManager) FindOrCreateSTAForSSID(ssid, passphrase, encryption, radio string) (string, error) {
	return gm.connector.FindOrCreateSTAForSSID(ssid, passphrase, encryption, radio)
}

func (gm *GatewayManager) RemoveDisabledSTA(ssid string) error {
	return gm.connector.RemoveDisabledSTA(ssid)
}

func (gm *GatewayManager) SwitchUpstream(activeIface, candidateIface, candidateSSID string) error {
	return gm.connector.SwitchUpstream(activeIface, candidateIface, candidateSSID)
}

func (gm *GatewayManager) GetSTADevice(ifaceName string) (string, error) {
	return gm.connector.GetSTADevice(ifaceName)
}

func (gm *GatewayManager) EnsureWWANSetup() error {
	return gm.connector.EnsureWWANSetup()
}

func (gm *GatewayManager) EnsureRadiosEnabled() error {
	return gm.connector.EnsureRadiosEnabled()
}

func (gm *GatewayManager) FormatScanResults(networks []NetworkInfo) string {
	if len(networks) == 0 {
		return "No networks found"
	}

	var result string
	result = fmt.Sprintf("%-30s %-15s %-20s %s\n", "SSID", "Signal", "Encryption", "Radio")
	result += fmt.Sprintf("%s\n", "----------------------------------------------------------------------")
	for _, net := range networks {
		result += fmt.Sprintf("%-30s %-15s %-20s %s\n", net.SSID, fmt.Sprintf("%d dBm", net.Signal), net.Encryption, net.Radio)
	}
	result += fmt.Sprintf("\n%d network(s) found.", len(networks))
	return result
}

func convertToStringMap(input map[string]interface{}) map[string]string {
	result := make(map[string]string)
	for key, value := range input {
		result[key] = fmt.Sprintf("%v", value)
	}
	return result
}

func init() {
	logger.WithField("module", "wireless_gateway_manager").Info("Wireless gateway manager module loaded")
}
