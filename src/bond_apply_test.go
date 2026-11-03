package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/upstream_detector"
)

// #978 fixes #974's disable path: a clean disable must FLUSH the kernel MPTCP
// endpoints a previous enabled boot left behind. Its test pins FlushScript's
// text — that the script contains `ip mptcp endpoint flush` and exits 0 when
// MPTCP is absent — which is worth having.
//
// But nothing pinned that the disable path CALLS it. Deleting the flush from
// main.go's else-branch left the whole suite green (verified): FlushScript is
// only reachable from tests, so #974's headline fix could be removed and the
// suite would still pass. Same shape as the rest of this sweep — the library is
// tested, the caller is not.
//
// applyBondConfig is the caller, extracted so it can be driven with a fake
// applier. These tests assert which script the disable path runs and that a
// failure is logged rather than swallowed.

// recordingApplier captures the scripts the bond path tries to run.
type recordingApplier struct {
	scripts []string
	err     error
}

func (a *recordingApplier) run(name string, arg ...string) error {
	if len(arg) > 0 {
		a.scripts = append(a.scripts, arg[len(arg)-1])
	}
	return a.err
}

func (a *recordingApplier) joined() string { return strings.Join(a.scripts, "\n---\n") }

// A disabled bond must flush. This is the assertion that fails if the flush
// call is removed from the disable path.
func TestDisabledBondFlushesKernelEndpoints(t *testing.T) {
	app := &recordingApplier{}
	var registered bool

	applyBondConfig([]byte(`{"enabled":false}`), app.run,
		func(upstream_detector.InterfaceEventSink) { registered = true }, &capturingLogger{})

	if len(app.scripts) != 1 {
		t.Fatalf("disable path ran %d script(s), want 1 (the flush): %q", len(app.scripts), app.joined())
	}
	if !strings.Contains(app.scripts[0], "ip mptcp endpoint flush") {
		t.Fatalf("disable path did not run a flush script:\n%s", app.scripts[0])
	}
	if registered {
		t.Error("a disabled bond must not register a reconciler")
	}
}

// An absent section is also a disable — #974's "section absent or enabled=false".
func TestAbsentBondSectionAlsoFlushes(t *testing.T) {
	app := &recordingApplier{}

	applyBondConfig([]byte(`{}`), app.run, func(upstream_detector.InterfaceEventSink) {}, &capturingLogger{})

	if len(app.scripts) != 1 || !strings.Contains(app.joined(), "ip mptcp endpoint flush") {
		t.Fatalf("an absent bond section must still flush (a previous boot may have enabled it); ran: %q", app.joined())
	}
}

// An enabled bond must register a reconciler and must NOT flush — flushing
// would tear down the endpoints it is about to manage.
func TestEnabledBondRegistersAndDoesNotFlush(t *testing.T) {
	app := &recordingApplier{}
	var registered bool

	// A config that passes Validate(): server host:port, method, password,
	// and at least two uplinks are all required when enabled.
	log := &capturingLogger{}
	cfg := []byte(`{"enabled":true,"server":"203.0.113.10:65101","method":"chacha20-ietf-poly1305",
		"password":"fixture-not-a-real-credential","policy":"guest_only","steering_cidr":"10.115.249.0/24","socks_port":1081,
		"tproxy_port":1082,"fwmark":"0x1","fwmark_table":100,
		"uplinks":[{"interface":"eth1","address":"192.0.2.10"},{"interface":"phy1-sta0","address":"198.51.100.7"}]}`)
	applyBondConfig(cfg, app.run, func(upstream_detector.InterfaceEventSink) { registered = true }, log)

	if !registered {
		t.Errorf("an enabled bond must register its reconciler (logged: %v)", log.all())
	}
	if strings.Contains(app.joined(), "ip mptcp endpoint flush") {
		t.Fatalf("an enabled bond must not flush its own endpoints:\n%s", app.joined())
	}
}

// A malformed section must change nothing — not register, not flush — because
// the bond is optional and a router that boots without it still serves
// customers.
func TestMalformedBondSectionTouchesNothing(t *testing.T) {
	app := &recordingApplier{}
	var registered bool

	applyBondConfig([]byte(`{"enabled":"yes please"}`), app.run,
		func(upstream_detector.InterfaceEventSink) { registered = true }, &capturingLogger{})

	if registered {
		t.Error("a malformed bond section must not register a reconciler")
	}
	if len(app.scripts) != 0 {
		t.Fatalf("a malformed bond section must not run scripts, ran: %q", app.joined())
	}
}

// A failed flush must be reported, not swallowed — the operator needs to know
// orphan endpoints survived a disable.
func TestFailedFlushIsReported(t *testing.T) {
	app := &recordingApplier{err: errors.New("ip: not found")}
	var logged []string
	logger := &capturingLogger{lines: &logged}

	applyBondConfig([]byte(`{"enabled":false}`), app.run, func(upstream_detector.InterfaceEventSink) {}, logger)

	joined := strings.Join(logged, "\n")
	if !strings.Contains(joined, "flush failed") {
		t.Fatalf("a failed flush must be logged loudly, got: %q", joined)
	}
}

// The shell indirection is real: the flush runs via `/bin/sh -c`. Assert the
// applier is invoked that way, so a future refactor to a direct syscall or a
// different interpreter is a deliberate change rather than an accident.
func TestFlushRunsThroughShell(t *testing.T) {
	var sawName string
	var sawArgs []string
	run := func(name string, arg ...string) error {
		sawName, sawArgs = name, arg
		return nil
	}

	applyBondConfig([]byte(`{"enabled":false}`), run, func(upstream_detector.InterfaceEventSink) {}, &capturingLogger{})

	if sawName != "/bin/sh" {
		t.Errorf("flush invoked %q, want /bin/sh", sawName)
	}
	if len(sawArgs) != 2 || sawArgs[0] != "-c" {
		t.Errorf("flush args = %q, want [-c <script>]", sawArgs)
	}
	if len(sawArgs) == 2 && !strings.Contains(sawArgs[1], "ip mptcp endpoint flush") {
		t.Errorf("flush script did not reach the shell:\n%s", sawArgs[1])
	}
}

// capturingLogger records what the bond path reports.
type capturingLogger struct {
	lines   *[]string
	own     []string
	pending error
}

func (l *capturingLogger) append(s string) {
	if l.pending != nil {
		s += " err=" + l.pending.Error()
		l.pending = nil
	}
	l.own = append(l.own, s)
	if l.lines != nil {
		*l.lines = append(*l.lines, s)
	}
}

// all returns everything logged, whether or not an external slice was given.
func (l *capturingLogger) all() []string {
	if l.lines != nil {
		return *l.lines
	}
	return l.own
}

func (l *capturingLogger) WithError(err error) bondLogger {
	l.pending = err
	return l
}
func (l *capturingLogger) Info(a ...interface{}) { l.append("info " + fmt.Sprint(a...)) }
func (l *capturingLogger) Infof(f string, a ...interface{}) {
	l.append("info " + fmt.Sprintf(f, a...))
}
func (l *capturingLogger) Warn(a ...interface{}) { l.append("warn " + fmt.Sprint(a...)) }
func (l *capturingLogger) Warnf(f string, a ...interface{}) {
	l.append("warn " + fmt.Sprintf(f, a...))
}
func (l *capturingLogger) Error(a ...interface{}) { l.append("error " + fmt.Sprint(a...)) }
func (l *capturingLogger) Errorf(f string, a ...interface{}) {
	l.append("error " + fmt.Sprintf(f, a...))
}
