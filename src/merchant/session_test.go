package merchant

import (
	"testing"
	"time"
)

func TestGetSessionRemovesExpiredMillisecondsSession(t *testing.T) {
	macAddress := "aa:bb:cc:dd:ee:ff"
	m := &Merchant{
		customerSessions: map[string]*CustomerSession{
			macAddress: {
				MacAddress: macAddress,
				StartTime:  time.Now().Add(-3 * time.Second).Unix(),
				Metric:     "milliseconds",
				Allotment:  1000,
			},
		},
	}

	session, err := m.GetSession(macAddress)
	if err == nil {
		t.Fatal("expected expired session lookup to fail")
	}
	if session != nil {
		t.Fatal("expected no session to be returned for expired session")
	}
	if _, exists := m.customerSessions[macAddress]; exists {
		t.Fatal("expected expired session to be removed from memory")
	}
}
