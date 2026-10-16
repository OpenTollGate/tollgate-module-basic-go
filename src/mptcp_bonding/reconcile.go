// The reconciler: keep the kernel's MPTCP state in step with reality.
//
// A bond on a mobile box is a moving target. A wireless uplink appears when the
// STA associates, disappears when it roams, and its address changes when DHCP
// renews. A script applied once at boot drifts from reality within minutes, and
// the failure is silent: the bond quietly stops using the interface that came
// back, and nobody notices until the throughput is wrong.
//
// The reconciler consumes interface/address events and re-applies the bond when
// the set of usable uplink addresses actually changes.
//
// Two rules keep it safe:
//
//  1. Only uplinks in the Config matter. A router has many interfaces that flap
//     (guest AP, loopback, container bridges); reacting to all of them would
//     thrash kernel state for no benefit.
//  2. Apply only when something CHANGED. Reconcile is called on every event, and
//     re-applying an identical script briefly perturbs the kernel for nothing.
//
// The side effect is injected as a function so this logic is testable without a
// router, and so the caller decides how the script is executed.
package mptcp_bonding

import (
	"fmt"
	"sort"
	"strings"
)

// Applier executes a rendered script. Implementations must be idempotent-safe:
// the reconciler may call this repeatedly and expects re-running to be harmless.
type Applier func(script string) error

// Reconciler keeps MPTCP endpoint state in step with interface events.
//
// It holds a working copy of the Config's uplink addresses: an uplink may be
// declared without an address and learn one later (DHCP, STA association), so
// the reconciler's view is the authoritative "what should be registered" set.
//
// Not safe for concurrent use: callers serialize events, which is how the
// network monitor already delivers them.
type Reconciler struct {
	cfg     *Config
	apply   Applier
	last    string          // last script successfully applied, to skip no-ops
	known   map[string]bool // interface -> currently registered
	ordered []string        // interface order, for deterministic rendering
}

// NewReconciler builds a reconciler for cfg that applies via apply.
//
// A nil apply is tolerated and turns every reconcile into a no-op, so a caller
// that only wants the decision logic (or has not wired execution yet) does not
// nil-panic on an event.
func NewReconciler(cfg *Config, apply Applier) *Reconciler {
	r := &Reconciler{
		cfg:   cfg,
		apply: apply,
		known: make(map[string]bool),
	}
	if cfg != nil {
		for _, u := range cfg.Uplinks {
			r.ordered = append(r.ordered, u.Interface)
			if strings.TrimSpace(u.Address) != "" {
				r.known[u.Interface] = true
			}
		}
	}
	return r
}

// isUplink reports whether name is one of the bond's declared members.
func (r *Reconciler) isUplink(name string) bool {
	if r.cfg == nil {
		return false
	}
	for _, u := range r.cfg.Uplinks {
		if u.Interface == name {
			return true
		}
	}
	return false
}

// setAddress records (or clears) the address of an uplink in the working copy.
// It returns true when this changed what should be registered.
func (r *Reconciler) setAddress(iface, addr string) bool {
	addr = strings.TrimSpace(addr)
	if r.cfg == nil || !r.isUplink(iface) {
		return false
	}

	// Update the working copy in place: the rendered plan reads from it.
	for i := range r.cfg.Uplinks {
		if r.cfg.Uplinks[i].Interface == iface {
			if r.cfg.Uplinks[i].Address == addr {
				// Unchanged; still ensure the known-set agrees.
				was := r.known[iface]
				now := addr != ""
				if was == now {
					return false
				}
				r.known[iface] = now
				return true
			}
			r.cfg.Uplinks[i].Address = addr
			break
		}
	}

	was := r.known[iface]
	now := addr != ""
	r.known[iface] = now
	return was != now
}

// HandleInterfaceUp is called when an interface comes up, optionally with a
// newly-learned address. It reconciles only if something changed.
func (r *Reconciler) HandleInterfaceUp(iface, addr string) error {
	if r.cfg == nil || !r.cfg.Enabled {
		return nil
	}
	if !r.isUplink(iface) {
		return nil
	}
	if r.setAddress(iface, addr) {
		return r.reconcile()
	}
	return nil
}

// HandleAddressAdded is called when an address appears on an interface. It is
// the common path for DHCP renewals and STA association.
func (r *Reconciler) HandleAddressAdded(iface, addr string) error {
	return r.HandleInterfaceUp(iface, addr)
}

// HandleInterfaceDown is called when an interface goes away. Its address must
// be withdrawn, or the kernel keeps advertising an address that is no longer
// reachable and the peer wastes JOIN attempts on it.
func (r *Reconciler) HandleInterfaceDown(iface string) error {
	if r.cfg == nil || !r.cfg.Enabled {
		return nil
	}
	if !r.isUplink(iface) {
		return nil
	}
	if r.setAddress(iface, "") {
		return r.reconcile()
	}
	return nil
}

// HandleAddressDeleted is called when an address is removed from an interface.
func (r *Reconciler) HandleAddressDeleted(iface, _ string) error {
	return r.HandleInterfaceDown(iface)
}

// reconcile renders the bond and applies it, unless the rendered script is
// identical to the last one applied.
//
// Skipping an identical apply is not just an optimisation: re-running the
// delete-then-add sequence tears down and rebuilds every endpoint, which would
// interrupt an in-flight subflow on every unrelated event.
func (r *Reconciler) reconcile() error {
	if r.cfg == nil || !r.cfg.Enabled {
		return nil
	}

	script := r.cfg.ApplyScript()
	if script == r.last {
		return nil
	}
	if r.apply == nil {
		return nil
	}
	if err := r.apply(script); err != nil {
		return fmt.Errorf("mptcp_bonding: applying bond: %w", err)
	}
	r.last = script
	return nil
}

// RegisteredUplinks returns the interfaces whose addresses are currently part
// of the bond, sorted for stable output in logs and tests.
func (r *Reconciler) RegisteredUplinks() []string {
	var out []string
	for iface, ok := range r.known {
		if ok {
			out = append(out, iface)
		}
	}
	sort.Strings(out)
	return out
}
