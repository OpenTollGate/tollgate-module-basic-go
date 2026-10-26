package main

import (
	"github.com/OpenTollGate/tollgate-module-basic-go/src/mptcp_bonding"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/upstream_detector"
	"github.com/sirupsen/logrus"
)

// bondLogger is the slice of the logger this path needs. Declaring it here
// (rather than taking *logrus.Entry) is what makes the disable path testable
// without a real logger, which is where #974's flush went unverified.
type bondLogger interface {
	WithError(error) bondLogger
	Info(...interface{})
	Infof(string, ...interface{})
	Warn(...interface{})
	Warnf(string, ...interface{})
	Error(...interface{})
	Errorf(string, ...interface{})
}

// bondApplier runs a rendered shell script. Injected so tests can assert WHICH
// script each branch runs without touching the kernel.
type bondApplier func(name string, arg ...string) error

// applyBondConfig performs the MPTCP bond decision at boot and reports it.
//
// It is a named function, called by initUpstreamDetector, so the branch that
// #974 is about — a clean disable must FLUSH the endpoints a previous enabled
// boot left behind — is reachable from a test. Before this, FlushScript was
// only called from here and only tested for its text, so deleting the call left
// the suite green.
//
// Three outcomes:
//
//   - malformed section: change nothing. The bond is optional and a router that
//     boots without it still serves customers, so this must not be fatal — but
//     it is logged loudly so the misconfiguration is not invisible.
//   - enabled: register the reconciler and reconcile what is already up. The
//     detector only reports CHANGES, so without the initial reconcile an uplink
//     that came up before the daemon started would never be registered.
//   - disabled (section absent, or enabled=false): flush. Idempotent, and a
//     no-op on a kernel without MPTCP.
func applyBondConfig(
	raw []byte,
	applier bondApplier,
	register func(upstream_detector.InterfaceEventSink),
	logger bondLogger,
) {
	cfg, err := mptcp_bonding.LoadConfig(raw)
	if err != nil {
		logger.WithError(err).Error("MPTCP bond config invalid; bond disabled")
		return
	}

	if !cfg.Enabled {
		// Disable-path teardown: idempotent; a no-op on kernels without MPTCP.
		if err := applier("/bin/sh", "-c", mptcp_bonding.FlushScript()); err != nil {
			logger.WithError(err).Warn("MPTCP bond disabled; endpoint flush failed")
			return
		}
		logger.Info("MPTCP bond disabled; kernel endpoints flushed")
		return
	}

	run := func(script string) error { return applier("/bin/sh", "-c", script) }
	reconciler := mptcp_bonding.NewReconciler(cfg, run)
	register(reconciler)

	if err := reconciler.ReconcileCurrent(); err != nil {
		logger.WithError(err).Error("MPTCP bond initial reconcile failed")
	}
	// #972 observability: the bond wires up late in boot (after merchant
	// construction), and early samples read as false negatives without a
	// positive marker.
	logger.Infof("MPTCP bond applied: %d uplink endpoint(s) from config", len(cfg.EndpointPlan()))
}

// logrusBondLogger adapts *logrus.Entry to bondLogger. logrus.Entry has every
// method, but WithError returns *logrus.Entry rather than the interface, so the
// adapter exists only to satisfy that one signature.
type logrusBondLogger struct{ e *logrus.Entry }

func (l logrusBondLogger) WithError(err error) bondLogger {
	return logrusBondLogger{l.e.WithError(err)}
}
func (l logrusBondLogger) Info(a ...interface{}) { l.e.Info(a...) }
func (l logrusBondLogger) Infof(f string, a ...interface{}) {
	l.e.Infof(f, a...)
}
func (l logrusBondLogger) Warn(a ...interface{}) { l.e.Warn(a...) }
func (l logrusBondLogger) Warnf(f string, a ...interface{}) {
	l.e.Warnf(f, a...)
}
func (l logrusBondLogger) Error(a ...interface{}) { l.e.Error(a...) }
func (l logrusBondLogger) Errorf(f string, a ...interface{}) {
	l.e.Errorf(f, a...)
}

var _ bondLogger = logrusBondLogger{}
