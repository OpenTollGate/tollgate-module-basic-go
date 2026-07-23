//go:build !cdk_wallet && testenv

package tollwallet

import (
	"strings"
	"testing"
)

func TestGonutsMeltQuotesNotStub(t *testing.T) {
	gw := &GonutsWallet{
		inner: &TollWallet{},
	}

	_, err := gw.RequestMeltQuote("invoice", "https://mint.example.com")
	if err != nil && strings.Contains(err.Error(), "not yet wired") {
		t.Fatalf("RequestMeltQuote still returns stub: %v", err)
	}

	_, err = gw.Melt("nonexistent")
	if err != nil && strings.Contains(err.Error(), "not yet wired") {
		t.Fatalf("Melt still returns stub: %v", err)
	}
}
