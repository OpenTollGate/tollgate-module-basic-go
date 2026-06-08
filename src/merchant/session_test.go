package merchant

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Origami74/gonuts-tollgate/cashu"
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

func TestGetSessionKeepsActiveMillisecondsSession(t *testing.T) {
	macAddress := "aa:bb:cc:dd:ee:ff"
	m := &Merchant{
		customerSessions: map[string]*CustomerSession{
			macAddress: {
				MacAddress: macAddress,
				StartTime:  time.Now().Add(-time.Second).Unix(),
				Metric:     "milliseconds",
				Allotment:  5000,
			},
		},
	}

	session, err := m.GetSession(macAddress)
	if err != nil {
		t.Fatalf("expected active session lookup to succeed, got %v", err)
	}
	if session == nil {
		t.Fatal("expected active session to be returned")
	}
	if _, exists := m.customerSessions[macAddress]; !exists {
		t.Fatal("expected active session to remain in memory")
	}
}

func TestGetSessionKeepsBytesSession(t *testing.T) {
	macAddress := "aa:bb:cc:dd:ee:ff"
	m := &Merchant{
		customerSessions: map[string]*CustomerSession{
			macAddress: {
				MacAddress: macAddress,
				StartTime:  time.Now().Add(-24 * time.Hour).Unix(),
				Metric:     "bytes",
				Allotment:  1024,
			},
		},
	}

	session, err := m.GetSession(macAddress)
	if err != nil {
		t.Fatalf("expected bytes session lookup to succeed, got %v", err)
	}
	if session == nil {
		t.Fatal("expected bytes session to be returned")
	}
}

func TestSpentTokenErrorCode(t *testing.T) {
	err := cashu.ProofAlreadyUsedErr

	var cashuErr cashu.Error
	if !errors.As(err, &cashuErr) {
		t.Fatal("expected errors.As to match cashu.Error")
	}
	if cashuErr.Code != cashu.ProofAlreadyUsedErrCode {
		t.Fatalf("expected code %d, got %d", cashu.ProofAlreadyUsedErrCode, cashuErr.Code)
	}
}

func TestSpentTokenErrorWithWrappedError(t *testing.T) {
	inner := fmt.Errorf("swap failed: %w", cashu.ProofAlreadyUsedErr)

	var cashuErr cashu.Error
	if !errors.As(inner, &cashuErr) {
		t.Fatal("expected errors.As to match cashu.Error through wrapped error")
	}
	if cashuErr.Code != cashu.ProofAlreadyUsedErrCode {
		t.Fatalf("expected code %d, got %d", cashu.ProofAlreadyUsedErrCode, cashuErr.Code)
	}
}

func TestNonCashuErrorNotMatched(t *testing.T) {
	err := fmt.Errorf("some random error")

	var cashuErr cashu.Error
	if errors.As(err, &cashuErr) {
		t.Fatal("expected errors.As to NOT match for non-cashu error")
	}
}

func TestOtherCashuErrorCodeNotMatched(t *testing.T) {
	err := cashu.InvalidProofErr

	var cashuErr cashu.Error
	if !errors.As(err, &cashuErr) {
		t.Fatal("expected errors.As to match cashu.Error")
	}
	if cashuErr.Code == cashu.ProofAlreadyUsedErrCode {
		t.Fatal("expected different error code, not ProofAlreadyUsedErrCode")
	}
}
