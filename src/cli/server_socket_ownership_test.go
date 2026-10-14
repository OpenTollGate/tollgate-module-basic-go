package cli

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The CLI socket is the daemon's command surface. Start() used to remove any
// existing socket file unconditionally, which let a second daemon steal the
// surface from a healthy first one (#504): the file vanished, the second
// daemon bound its own socket, and every `tollgate` command executed against
// the wallet-less process. These tests pin the three ownership cases.

// listenTestSocket binds a unix listener at path — the stand-in for a
// running daemon's CLI server.
func listenTestSocket(path string) (net.Listener, error) {
	return net.Listen("unix", path)
}

func TestStartRefusesToReplaceALiveDaemonsSocket(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", dir)
	sock := filepath.Join(dir, "tollgate.sock")

	// A "running daemon": a listener that accepts on the socket path.
	daemon, err := listenTestSocket(sock)
	if err != nil {
		t.Fatalf("starting the incumbent daemon listener: %v", err)
	}
	defer daemon.Close()

	s := NewCLIServer(nil, nil, nil, nil, nil)
	err = s.Start()
	if err == nil {
		s.Stop()
		t.Fatal("Start replaced a live daemon's CLI socket — the double-start is silent again")
	}
	if !strings.Contains(err.Error(), "already serving") {
		t.Fatalf("the refusal must tell the operator another daemon owns the socket, got: %v", err)
	}
	// The incumbent's socket file must still exist — the refusal must not
	// have removed it on the way out.
	if _, statErr := os.Stat(sock); statErr != nil {
		t.Fatalf("the refusal removed the live daemon's socket file: %v", statErr)
	}
}

func TestStartReclaimsAStaleSocketFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", dir)
	sock := filepath.Join(dir, "tollgate.sock")

	// Crash debris: the socket file exists, nothing listens on it.
	if err := os.WriteFile(sock, []byte{}, 0600); err != nil {
		t.Fatalf("planting stale socket debris: %v", err)
	}

	s := NewCLIServer(nil, nil, nil, nil, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("Start must reclaim a stale socket file (crashed daemon), got: %v", err)
	}
	defer s.Stop()
	if _, statErr := os.Stat(sock); statErr != nil {
		t.Fatalf("the reclaimed socket must be bound: %v", statErr)
	}
}

func TestStartBindsWhenNoSocketExists(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", dir)

	s := NewCLIServer(nil, nil, nil, nil, nil)
	if err := s.Start(); err != nil {
		t.Fatalf("Start on a clean socket path: %v", err)
	}
	defer s.Stop()
}

// TestConcurrentStartsElectExactlyOneOwner is the regression fence for the
// probe-then-remove race: several daemons racing Start() on one socket
// path must elect exactly one winner; every loser refuses with "already
// serving" without removing the winner's socket, which still serves.
func TestConcurrentStartsElectExactlyOneOwner(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TOLLGATE_TEST_CONFIG_DIR", dir)
	sock := filepath.Join(dir, "tollgate.sock")

	const contenders = 16
	servers := make([]*CLIServer, contenders)
	type startResult struct {
		idx int
		err error
	}
	results := make(chan startResult, contenders)
	start := make(chan struct{})

	for i := range servers {
		servers[i] = NewCLIServer(nil, nil, nil, nil, nil)
		go func() {
			<-start
			results <- startResult{idx: i, err: servers[i].Start()}
		}()
	}
	close(start)

	winner := -1
	for range servers {
		res := <-results
		if res.err == nil {
			if winner != -1 {
				t.Fatalf("a second daemon won the socket race (contender %d after %d)", res.idx, winner)
			}
			winner = res.idx
		} else if !strings.Contains(res.err.Error(), "already serving") {
			t.Fatalf("loser start must refuse with the already-serving error, got: %v", res.err)
		}
	}
	if winner == -1 {
		t.Fatal("no contender won the socket race — every start refused")
	}

	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		t.Fatalf("the winner's socket does not serve after the losers' attempts (it was stolen or removed): %v", err)
	}
	conn.Close()

	servers[winner].Stop()
	if _, statErr := os.Stat(sock); !os.IsNotExist(statErr) {
		t.Fatalf("socket file must be gone after the winner's Stop: %v", statErr)
	}
}
