package nwc

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

func TestClientValueFormattingRedactsSpendingSecret(t *testing.T) {
	secret := nostr.GeneratePrivateKey()
	wallet, err := nostr.GetPublicKey(nostr.GeneratePrivateKey())
	if err != nil {
		t.Fatal("generate synthetic wallet")
	}
	query := url.Values{"secret": {secret}, "relay": {"wss://relay.invalid"}}
	uri := "nostr+walletconnect://" + wallet + "?" + query.Encode()
	c, err := New(Config{ConnectionURI: uri})
	if err != nil {
		t.Fatal("construct synthetic client")
	}

	// Deliberately format an inert value, before any mutex use. Reflection
	// reproduces a generic logger's value path without copying a live mutex
	// in application code or disabling vet's copylocks check.
	value := reflect.ValueOf(c).Elem()
	wrapper := reflect.New(reflect.TypeOf(struct{ Wallet Client }{})).Elem()
	wrapper.Field(0).Set(value)
	for _, shape := range []struct {
		name  string
		value any
	}{
		{"pointer", c},
		{"value", value.Interface()},
		{"embedded-value", wrapper.Interface()},
	} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			t.Run(shape.name+"/"+format, func(t *testing.T) {
				formatted := fmt.Sprintf(format, shape.value)
				if strings.Contains(formatted, secret) || strings.Contains(formatted, uri) || strings.Contains(formatted, "secret=") {
					t.Fatal("client formatting disclosed synthetic connection material")
				}
			})
		}
	}
}
