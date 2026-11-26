package lnurlcash

import (
	"io"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

// testTimeout bounds every issuer call in the tests. Long enough for a local
// httptest round trip under -race, short enough that the timeout cases do not
// dominate the suite.
const testTimeout = 2 * time.Second

// captureLogs redirects the standard logger into buf for the duration of a test.
// The redeemer logs through log.Printf (matching the merchant package's style),
// so this is what an operator's log actually receives.
func captureLogs(buf *strings.Builder) func() {
	prevOut := log.Writer()
	prevFlags := log.Flags()
	prevPrefix := log.Prefix()

	pr, pw := io.Pipe()
	log.SetOutput(pw)
	log.SetFlags(0)
	log.SetPrefix("")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		io.Copy(buf, pr)
	}()

	return func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
		log.SetPrefix(prevPrefix)
		_ = pw.Close()
		wg.Wait()
	}
}

var _ = testing.Short
