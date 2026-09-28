package merchant

import (
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

// One address, three spellings. The HTTP layer forwards the `mac` query
// parameter as the client sent it, so every casing must resolve to the same
// session and the same lightning quote. Sessions and quotes are keyed by MAC
// strings, and the REST of the system (nodogsplash preauth, the DHCP-lease and
// ARP lookup behind getMacAddress, /whoami) is lowercase — a client that
// uppercases the parameter must not see an existing quote as "not found".
const (
	macCaseLower = "8c:16:45:0d:6f:c5"
	macCaseUpper = "8C:16:45:0D:6F:C5"
	macCaseMixed = "8C:16:45:0d:6F:C5"
)

func macCasings() []struct {
	name string
	mac  string
} {
	return []struct {
		name string
		mac  string
	}{
		{"lowercase", macCaseLower},
		{"uppercase", macCaseUpper},
		{"mixed-case", macCaseMixed},
	}
}

func TestGetSessionMacLookupIsCaseInsensitive(t *testing.T) {
	for _, tc := range macCasings() {
		t.Run(tc.name, func(t *testing.T) {
			m := &Merchant{
				customerSessions: map[string]*CustomerSession{
					macCaseLower: {
						MacAddress: macCaseLower,
						StartTime:  time.Now().Unix(),
						Metric:     "milliseconds",
						Allotment:  5000,
					},
				},
			}

			session, err := m.GetSession(tc.mac)
			if err != nil {
				t.Fatalf("GetSession(%q) must resolve the session stored under %q: %v", tc.mac, macCaseLower, err)
			}
			if session == nil {
				t.Fatalf("GetSession(%q) returned no session for %q", tc.mac, macCaseLower)
			}
			if session.Allotment != 5000 {
				t.Fatalf("GetSession(%q) returned allotment %d, want 5000", tc.mac, session.Allotment)
			}
		})
	}
}

func TestAddAllotmentKeyIsCaseInsensitive(t *testing.T) {
	for _, tc := range macCasings() {
		t.Run(tc.name, func(t *testing.T) {
			m := &Merchant{
				customerSessions: map[string]*CustomerSession{
					macCaseLower: {
						MacAddress: macCaseLower,
						StartTime:  time.Now().Unix(),
						Metric:     "milliseconds",
						Allotment:  5000,
					},
				},
			}

			session, err := m.AddAllotment(tc.mac, "milliseconds", 2000)
			if err != nil {
				t.Fatalf("AddAllotment(%q) failed: %v", tc.mac, err)
			}
			if session.Allotment != 7000 {
				t.Fatalf("AddAllotment(%q) extended allotment to %d, want 7000 (the existing %q session)", tc.mac, session.Allotment, macCaseLower)
			}

			m.sessionMu.RLock()
			stored, ok := m.customerSessions[macCaseLower]
			entryCount := len(m.customerSessions)
			m.sessionMu.RUnlock()

			if !ok {
				t.Fatalf("AddAllotment(%q) lost the session stored under %q", tc.mac, macCaseLower)
			}
			if stored.Allotment != 7000 {
				t.Fatalf("session under %q has allotment %d, want 7000", macCaseLower, stored.Allotment)
			}
			if entryCount != 1 {
				t.Fatalf("AddAllotment(%q) created a duplicate session entry: %d entries, want 1", tc.mac, entryCount)
			}
		})
	}
}

func TestGetUsageMacLookupIsCaseInsensitive(t *testing.T) {
	for _, tc := range macCasings() {
		t.Run(tc.name, func(t *testing.T) {
			m := &Merchant{
				customerSessions: map[string]*CustomerSession{
					macCaseLower: {
						MacAddress: macCaseLower,
						StartTime:  time.Now().Unix(),
						Metric:     "milliseconds",
						Allotment:  5000,
					},
				},
			}

			usage, err := m.GetUsage(tc.mac)
			if err != nil {
				t.Fatalf("GetUsage(%q) failed: %v", tc.mac, err)
			}
			if usage == "-1/-1" {
				t.Fatalf("GetUsage(%q) returned %q — no session found for a MAC that has one", tc.mac, usage)
			}
		})
	}
}

func TestGetLightningQuoteRecordForMACIsCaseInsensitive(t *testing.T) {
	for _, tc := range macCasings() {
		t.Run(tc.name, func(t *testing.T) {
			m := &Merchant{
				lightningQuotes: map[string]*lightningQuoteRecord{
					"quote-1": {
						MacAddress: macCaseLower,
						MintURL:    "https://mint.example.com",
						Amount:     10,
					},
				},
			}

			record, err := m.getLightningQuoteRecordForMAC("quote-1", tc.mac)
			if err != nil {
				t.Fatalf("getLightningQuoteRecordForMAC(%q) must find the quote bound to %q: %v", tc.mac, macCaseLower, err)
			}
			if record.MacAddress != macCaseLower {
				t.Fatalf("quote record MAC is %q, want %q", record.MacAddress, macCaseLower)
			}
		})
	}
}

func TestGetLightningInvoiceStatusMacLookupIsCaseInsensitive(t *testing.T) {
	for _, tc := range macCasings() {
		t.Run(tc.name, func(t *testing.T) {
			m := &Merchant{
				config: &config_manager.Config{Metric: "milliseconds"},
				lightningQuotes: map[string]*lightningQuoteRecord{
					"quote-1": {
						MacAddress:     macCaseLower,
						MintURL:        "https://mint.example.com",
						Amount:         10,
						CachedState:    tollwallet.StateUnpaid,
						CachedStateAt:  time.Now(),
						HasCachedState: true,
					},
				},
			}

			status, err := m.GetLightningInvoiceStatus("quote-1", tc.mac)
			if err != nil {
				t.Fatalf("GetLightningInvoiceStatus(%q) must answer for the quote bound to %q: %v", tc.mac, macCaseLower, err)
			}
			if status.State != tollwallet.StateUnpaid.String() {
				t.Fatalf("state is %q, want %q", status.State, tollwallet.StateUnpaid.String())
			}
		})
	}
}
