package wireless_gateway_manager

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"
)

const (
	tollgateOUI               = "212121"
	tollgateElemType          = "01"
	tollgateElemTypeByte byte = 0x01

	flagIsReseller  = 0x01
	flagHasInternet = 0x02
	flagOpenNetwork = 0x04

	tlvTypeMintURL = 0x01
	tlvTypePubkey  = 0x02
	// Pricing TLVs: the price a customer pays per step, and the step size
	// (the metric quantum). Both are 2-byte big-endian so a scan-side parser
	// can read them without a signed advertisement round-trip. The signed
	// kind-10021 advertisement served on :2121 remains the authority; these
	// are an unsigned hint that selects candidates for the signed probe.
	tlvTypePricePerStep = 0x03
	tlvTypeStepSize     = 0x04
)

// EncodeTollGateVendorIE serializes the advertisement as one vendor IE.
// The IE is an unsigned hint — the signed advertisement served on :2121 is
// the authority — so when the body would exceed the 255-byte element cap
// the mint TLV (the bulkiest optional field) is dropped and reported via
// the truncated flag instead of failing or silently diverging. Only a
// pubkey that cannot fit even without the mint TLV is an error.
func EncodeTollGateVendorIE(adv TollGateAdvertisement) (string, bool, error) {
	var flags uint8
	if adv.IsReseller {
		flags |= flagIsReseller
	}
	if adv.HasInternet {
		flags |= flagHasInternet
	}
	if adv.OpenNetwork {
		flags |= flagOpenNetwork
	}

	oui, err := hex.DecodeString(tollgateOUI)
	if err != nil {
		return "", false, fmt.Errorf("invalid OUI hex: %w", err)
	}

	elemType, _ := hex.DecodeString(tollgateElemType)
	body := append(oui, elemType...)
	body = append(body, adv.Version, flags)

	truncated := false
	pubkeyLen := len(adv.Pubkey)
	mintBytes := []byte(adv.MintURL)
	if adv.MintURL != "" {
		withMint := len(body) + 2 + len(mintBytes) + pubkeyTailLen(pubkeyLen)
		if len(mintBytes) > 255 || withMint > 255 {
			truncated = true
		} else {
			body = append(body, tlvTypeMintURL, uint8(len(mintBytes)))
			body = append(body, mintBytes...)
		}
	}

	// Pricing TLVs, 2-byte big-endian each. Emitted only when set, and each is
	// dropped (with truncated=true) rather than pushing out the pubkey or
	// overflowing the element: identification outranks the pricing hint.
	//
	// ORDER MATTERS. Pricing is emitted BEFORE the pubkey. A pubkey can be up to
	// 251 bytes and still pass the fit check below, landing the body at 253-255;
	// if pricing were appended after it, the 4-byte guard would fire and the
	// pricing would be silently dropped while the element still claimed success.
	// Ordering pricing first means an oversized pubkey instead fails the LOUD
	// error path, so a caller never believes a price was advertised when it
	// was not. (Defect found by a cold cross-family review of the original
	// ordering, 2026-10-16.)
	for _, tlv := range []struct {
		typ byte
		val uint16
	}{
		{tlvTypePricePerStep, adv.PricePerStep},
		{tlvTypeStepSize, adv.StepSize},
	} {
		if tlv.val == 0 {
			continue
		}
		if len(body)+4 > 255 {
			truncated = true
			continue
		}
		body = append(body, tlv.typ, 0x02, byte(tlv.val>>8), byte(tlv.val))
	}

	if pubkeyLen > 0 {
		if len(body)+2+pubkeyLen > 255 {
			return "", false, fmt.Errorf("vendor IE body too long: pubkey %d bytes cannot fit (body so far %d, max 255)", pubkeyLen, len(body))
		}
		body = append(body, tlvTypePubkey, uint8(pubkeyLen))
		body = append(body, adv.Pubkey...)
	}

	// The length octet is a single byte. It can never legitimately exceed 255
	// because every append above is guarded, but a silent truncation here would
	// emit a malformed element that a peer could misparse, so the wrap is made
	// explicit and reported instead of assumed unreachable.
	if len(body) > 255 {
		truncated = true
		body = body[:255]
	}

	ie := append([]byte{0xDD, uint8(len(body))}, body...)
	return hex.EncodeToString(ie), truncated, nil
}

// pubkeyTailLen is the encoded size of the pubkey TLV for a key of n bytes
// (0 when there is no key).
func pubkeyTailLen(n int) int {
	if n == 0 {
		return 0
	}
	return 2 + n
}

func ParseTollGateVendorIE(raw []byte) *TollGateAdvertisement {
	if len(raw) < 8 {
		return nil
	}
	if raw[0] != 0xDD {
		return nil
	}

	bodyLen := int(raw[1])
	if len(raw) < 2+bodyLen {
		return nil
	}
	body := raw[2 : 2+bodyLen]

	oui := fmt.Sprintf("%02x%02x%02x", body[0], body[1], body[2])
	if oui != tollgateOUI {
		return nil
	}
	if body[3] != tollgateElemTypeByte {
		return nil
	}

	adv := &TollGateAdvertisement{
		Version: body[4],
	}

	if len(body) > 5 {
		flags := body[5]
		adv.IsReseller = flags&flagIsReseller != 0
		adv.HasInternet = flags&flagHasInternet != 0
		adv.OpenNetwork = flags&flagOpenNetwork != 0
	}

	tlvOffset := 6
	for tlvOffset+2 <= len(body) {
		tlvType := body[tlvOffset]
		tlvLen := int(body[tlvOffset+1])
		if tlvOffset+2+tlvLen > len(body) {
			break
		}
		tlvValue := body[tlvOffset+2 : tlvOffset+2+tlvLen]

		switch tlvType {
		case tlvTypeMintURL:
			adv.MintURL = string(tlvValue)
		case tlvTypePubkey:
			adv.Pubkey = make([]byte, len(tlvValue))
			copy(adv.Pubkey, tlvValue)
		case tlvTypePricePerStep:
			if len(tlvValue) == 2 {
				adv.PricePerStep = uint16(tlvValue[0])<<8 | uint16(tlvValue[1])
			}
		case tlvTypeStepSize:
			if len(tlvValue) == 2 {
				adv.StepSize = uint16(tlvValue[0])<<8 | uint16(tlvValue[1])
			}
		}

		tlvOffset += 2 + tlvLen
	}

	return adv
}

func ParseVendorIEsFromScanData(raw []byte) []*TollGateAdvertisement {
	var results []*TollGateAdvertisement

	offset := 0
	for offset+1 < len(raw) {
		elemID := raw[offset]
		length := int(raw[offset+1])

		if offset+2+length > len(raw) {
			break
		}

		if elemID == 0xDD && length >= 4 {
			ieBytes := raw[offset : offset+2+length]
			if adv := ParseTollGateVendorIE(ieBytes); adv != nil {
				results = append(results, adv)
			}
		}

		offset += 2 + length
	}

	return results
}

func (v *VendorElementProcessor) ExtractAndScore(ni NetworkInfo) (map[string]interface{}, int, error) {
	vendorElements := make(map[string]interface{})
	score := v.calculateScore(ni, vendorElements)
	return vendorElements, score, nil
}

func (v *VendorElementProcessor) calculateScore(ni NetworkInfo, vendorElements map[string]interface{}) int {
	score := ni.Signal

	// SSID heuristic: small boost for a brand-prefix name (TollGate-… or the
	// whitelabel re-brand's prefix, case-insensitively — see hasTollGateSSID).
	// This is a weak signal (easily spoofed) so the boost is intentionally small.
	if hasTollGateSSID(ni.SSID) {
		score += 10
	}

	// Vendor IE (definitive identification via OUI + TLV structure).
	// Conditional boost: only prefer TollGate APs with usable signal.
	// Research across macOS (-75 dBm trigger), iOS (-70 dBm), Android (-73/-70 dBm
	// thresholds), Cisco (6 dB hysteresis), and Aruba (5-10 dB delta bound) shows
	// that real-world hysteresis is 8-12 dB. A +200 boost was 17-40x larger than any
	// production system. See #311 for full analysis and roadmap.
	if ni.IsTollGate && ni.Signal > -75 {
		score += 15
	}

	return score
}

// VendorElementProcessor owns the WRITE path: telling hostapd which
// vendor-specific information elements this AP should beacon. Before this, the
// whole emit chain was unreachable (EmitTollGateVendorIE had no callers and the
// reader below returned an empty map), so no router could advertise itself and
// the scan-side producer had nothing to find.
type VendorElementProcessor struct {
	connector *Connector

	// disabled makes the processor inert. Wired from
	// config.UpstreamWifi.VendorIEDiscovery so that merging the emitter changes
	// nothing on an existing deployment until the operator opts in.
	disabled bool

	// foreignElements holds vendor elements this processor did NOT set, keyed
	// by a stable name, so they can be re-sent alongside ours. Writing only our
	// own hex would clobber co-resident elements (11k/11v, mesh, other tooling)
	// because hostapd's set_vendor_elements REPLACES the whole set.
	foreignElements map[string]string

	// applied records the last payload successfully applied, so
	// GetLocalAPVendorElements can report what is actually on the AP instead of
	// an empty map.
	applied map[string]string
}

// SetLocalAPVendorElements pushes the given elements to every hostapd
// interface, PRESERVING any foreign elements previously observed.
//
// Returns an error when hostapd interfaces exist but every write to them
// failed. That case used to return nil, which meant a router could fail to
// advertise and report success — indistinguishable from a router that simply
// has nothing to say.
func (v *VendorElementProcessor) SetLocalAPVendorElements(elements map[string]string) error {
	if v.disabled {
		logger.Debug("SetLocalAPVendorElements: vendor-IE discovery disabled; not writing")
		return nil
	}

	logger.WithFields(logrus.Fields{
		"elements": elements,
	}).Debug("SetLocalAPVendorElements: setting vendor elements on local AP")

	output, err := v.connector.ExecuteUbus("list")
	if err != nil {
		logger.WithError(err).Warn("SetLocalAPVendorElements: ubus list failed, hostapd may not be running")
		return nil
	}

	var hostapdIfaces []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "hostapd.") {
			hostapdIfaces = append(hostapdIfaces, line)
		}
	}

	if len(hostapdIfaces) == 0 {
		// A router with its radio down is a legitimate state, not a failure.
		logger.Warn("SetLocalAPVendorElements: no hostapd interfaces found")
		return nil
	}

	// Our elements first, then everything we do not own, so a foreign element
	// survives. Ordering is irrelevant to hostapd; inclusion is not.
	merged := make(map[string]string, len(elements)+len(v.foreignElements))
	for k, val := range v.foreignElements {
		merged[k] = val
	}
	for k, val := range elements {
		merged[k] = val
	}

	// Deterministic order so the payload is stable run to run (a set of
	// elements with the same content must produce the same bytes).
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var hexIEs []string
	for _, k := range keys {
		hexIEs = append(hexIEs, merged[k])
	}
	combinedHex := strings.Join(hexIEs, "")

	var attempted, failed int
	for _, iface := range hostapdIfaces {
		payload := map[string]string{"vendor_elements": combinedHex}
		jsonBytes, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal vendor elements payload: %w", err)
		}
		attempted++
		_, err = v.connector.ExecuteUbus("call", iface, "set_vendor_elements", string(jsonBytes))
		if err != nil {
			failed++
			logger.WithFields(logrus.Fields{
				"interface": iface,
				"error":     err,
			}).Warn("SetLocalAPVendorElements: failed to set vendor elements on interface")
			continue
		}
		logger.WithField("interface", iface).Info("SetLocalAPVendorElements: vendor elements set")
	}

	if attempted > 0 && failed == attempted {
		return fmt.Errorf("failed to set vendor elements on all %d hostapd interface(s)", attempted)
	}

	// Record what we applied, so the reader can report the AP's real state.
	if v.applied == nil {
		v.applied = make(map[string]string, len(elements))
	}
	for k, val := range elements {
		v.applied[k] = val
	}

	return nil
}

// GetLocalAPVendorElements reports the elements this processor last applied.
//
// LIMITATION, stated so nobody trusts it further than it goes: this is the
// processor's own record of the last successful write, NOT a read-back from
// hostapd. OpenWrt's hostapd exposes set_vendor_elements but no getter, so a
// true read-back is not possible over ubus. A third party changing the elements
// behind us would not be reflected here.
func (v *VendorElementProcessor) GetLocalAPVendorElements() (map[string]string, error) {
	out := make(map[string]string, len(v.applied))
	for k, val := range v.applied {
		out[k] = val
	}
	return out, nil
}

// NewVendorElementProcessor builds a processor. It defaults to DISABLED so that
// introducing the emitter changes nothing on an existing deployment; the daemon
// passes the vendor_ie_discovery config flag to opt in.
func NewVendorElementProcessor(connector *Connector, enabled ...bool) *VendorElementProcessor {
	p := &VendorElementProcessor{connector: connector, disabled: true}
	if len(enabled) > 0 && enabled[0] {
		p.disabled = false
	}
	return p
}

// EmitTollGateVendorIE encodes the advertisement and pushes it to hostapd.
// It is a no-op when vendor-IE discovery is disabled.
func EmitTollGateVendorIE(processor *VendorElementProcessor, adv TollGateAdvertisement) error {
	if processor == nil || processor.disabled {
		return nil
	}
	ieHex, truncated, err := EncodeTollGateVendorIE(adv)
	if err != nil {
		return fmt.Errorf("failed to encode vendor IE: %w", err)
	}
	if truncated {
		logger.WithFields(logrus.Fields{
			"mint_url_len": len(adv.MintURL),
			"pubkey_len":   len(adv.Pubkey),
		}).Warn("EmitTollGateVendorIE: body exceeded the 255-byte cap — mint TLV dropped; the signed advertisement on :2121 remains authoritative")
	}

	elements := map[string]string{"tollgate": ieHex}
	return processor.SetLocalAPVendorElements(elements)
}
