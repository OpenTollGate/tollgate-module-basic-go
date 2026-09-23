package tollwallet

import (
	"net"
	"testing"
	"time"
)

// silentMint is a TCP endpoint that accepts connections and never answers —
// a wedged mint (incident #525's class). Naming follows the closed #532's
// vocabulary so the helpers can be unified when that idiom re-lands.
func silentMint(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			// Hold the connection open without ever responding.
			go func(c net.Conn) {
				buf := make([]byte, 512)
				for {
					if _, err := c.Read(buf); err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	return "http://" + listener.Addr().String()
}

// TestCheckTokenSpendable_WedgedMintIsBounded pins the recover command's
// liveness check against wedged mints: a mint that accepts the TCP
// connection but never answers must produce an error within the budget,
// not park the caller for the HTTP client's full retry ladder.
func TestCheckTokenSpendable_WedgedMintIsBounded(t *testing.T) {
	oldBudget := checkStateBudget
	checkStateBudget = 300 * time.Millisecond
	t.Cleanup(func() { checkStateBudget = oldBudget })

	token := buildTestToken(t, silentMint(t), []string{"wedged-secret"})

	start := time.Now()
	_, err := CheckTokenSpendable(token)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("wedged mint must yield an error, not a verdict")
	}
	if elapsed > 3*checkStateBudget {
		t.Fatalf("checkstate against a wedged mint took %s; the budget (%s) did not apply", elapsed, checkStateBudget)
	}
}
