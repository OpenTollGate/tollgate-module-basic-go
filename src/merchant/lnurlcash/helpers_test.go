package lnurlcash

import (
	"bytes"
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

// captureLogs redirects the standard logger into buf for the duration of a test,
// and returns the function that restores it.
//
// Two details that are load-bearing under -race:
//
//   - The writes go through a mutex-guarded buffer rather than the strings.Builder
//     the caller holds. log.Logger serialises its own writes, but the READER is a
//     different goroutine from the test, and a strings.Builder is not safe for
//     concurrent write+read. That was a real race this helper used to produce.
//   - Restore waits for the writer. Otherwise the copy goroutine can still be
//     appending while the next test reads the buffer, which the race detector
//     reports against the following test — a failure that looks like someone
//     else's bug.
func captureLogs(buf *strings.Builder) func() {
	logMu.Lock()
	prevOut := log.Writer()
	prevFlags := log.Flags()
	prevPrefix := log.Prefix()

	var captured bytes.Buffer
	var mu sync.Mutex
	log.SetOutput(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return captured.Write(p)
	}))
	log.SetFlags(0)
	log.SetPrefix("")
	logMu.Unlock()

	return func() {
		logMu.Lock()
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
		log.SetPrefix(prevPrefix)
		logMu.Unlock()

		mu.Lock()
		defer mu.Unlock()
		buf.Write(captured.Bytes())
	}
}

// logMu serialises captureLogs itself. Two tests running in parallel would
// otherwise interleave their swap and restore and one would silently capture
// nothing — a test that passes because it saw no logs at all.
var logMu sync.Mutex

// writerFunc adapts a function to io.Writer without pulling io in for one type.
type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

var _ = testing.Short
