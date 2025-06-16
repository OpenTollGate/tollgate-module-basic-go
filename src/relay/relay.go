package relay

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/fiatjaf/khatru"
	"github.com/nbd-wtf/go-nostr"
)

// TollGateKinds defines the event kinds accepted by this relay
var TollGateKinds = map[int]bool{
	21000: true, // Payment events
	10021: true, // TollGate Discovery events
	1022:  true, // Session events
	21023: true, // Notice events
}

// PrivateRelay represents an in-memory Khatru-based relay for TollGate events
type PrivateRelay struct {
	relay *khatru.Relay
	store map[string]*nostr.Event
	mutex sync.RWMutex
}

// NewPrivateRelay creates a new private relay instance
func NewPrivateRelay() *PrivateRelay {
	pr := &PrivateRelay{
		relay: khatru.NewRelay(),
		store: make(map[string]*nostr.Event),
	}

	pr.setupRelay()
	return pr
}

// setupRelay configures the Khatru relay with TollGate-specific policies
func (pr *PrivateRelay) setupRelay() {
	// Set up basic relay info
	pr.relay.Info.Name = "TollGate Private Relay"
	pr.relay.Info.Description = "In-memory relay for TollGate protocol events"
	pr.relay.Info.Version = "v0.0.1"

	// Configure event storage
	pr.relay.StoreEvent = append(pr.relay.StoreEvent, pr.storeEvent)

	// Configure event querying
	pr.relay.QueryEvents = append(pr.relay.QueryEvents, pr.queryEvents)

	// Configure event deletion
	pr.relay.DeleteEvent = append(pr.relay.DeleteEvent, pr.deleteEvent)

	// Set up rejection policies
	pr.relay.RejectEvent = append(pr.relay.RejectEvent, pr.validateTollGateKind)

	log.Println("Private relay configured for TollGate protocol events")
}

// storeEvent stores an event in the in-memory store
func (pr *PrivateRelay) storeEvent(ctx context.Context, event *nostr.Event) error {
	pr.mutex.Lock()
	defer pr.mutex.Unlock()

	pr.store[event.ID] = event
	log.Printf("Stored event in private relay: %s (kind: %d)", event.ID, event.Kind)
	return nil
}

// queryEvents queries events from the in-memory store
func (pr *PrivateRelay) queryEvents(ctx context.Context, filter nostr.Filter) (chan *nostr.Event, error) {
	pr.mutex.RLock()
	events := make([]*nostr.Event, 0)

	for _, event := range pr.store {
		if filter.Matches(event) {
			events = append(events, event)
		}
	}
	pr.mutex.RUnlock()

	ch := make(chan *nostr.Event)
	go func() {
		defer close(ch)
		for _, event := range events {
			select {
			case ch <- event:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, nil
}

// deleteEvent removes an event from the in-memory store
func (pr *PrivateRelay) deleteEvent(ctx context.Context, event *nostr.Event) error {
	pr.mutex.Lock()
	defer pr.mutex.Unlock()

	delete(pr.store, event.ID)
	log.Printf("Deleted event from private relay: %s", event.ID)
	return nil
}

// validateTollGateKind validates that only TollGate protocol events are accepted
func (pr *PrivateRelay) validateTollGateKind(ctx context.Context, event *nostr.Event) (reject bool, msg string) {
	if !TollGateKinds[event.Kind] {
		return true, fmt.Sprintf("event kind %d not supported by TollGate protocol", event.Kind)
	}
	return false, ""
}

// GetRelay returns the underlying Khatru relay instance
func (pr *PrivateRelay) GetRelay() *khatru.Relay {
	return pr.relay
}

// Start starts the relay on the specified address
func (pr *PrivateRelay) Start(addr string) error {
	log.Printf("Starting TollGate private relay on %s", addr)
	return http.ListenAndServe(addr, pr.relay)
}

// PublishEvent publishes an event to the relay
func (pr *PrivateRelay) PublishEvent(event *nostr.Event) error {
	// Validate event signature
	ok, err := event.CheckSignature()
	if err != nil || !ok {
		return fmt.Errorf("invalid event signature: %v", err)
	}

	// Store the event
	return pr.storeEvent(context.Background(), event)
}

// QueryEvents queries events from the relay
func (pr *PrivateRelay) QueryEvents(filter nostr.Filter) ([]*nostr.Event, error) {
	ch, err := pr.queryEvents(context.Background(), filter)
	if err != nil {
		return nil, err
	}

	var events []*nostr.Event
	for event := range ch {
		events = append(events, event)
	}

	return events, nil
}

// GetEventCount returns the number of events stored in the relay
func (pr *PrivateRelay) GetEventCount() int {
	pr.mutex.RLock()
	defer pr.mutex.RUnlock()
	return len(pr.store)
}

// Clear removes all events from the relay
func (pr *PrivateRelay) Clear() {
	pr.mutex.Lock()
	defer pr.mutex.Unlock()
	pr.store = make(map[string]*nostr.Event)
	log.Println("Cleared all events from private relay")
}

// GetAllEvents returns all events stored in the relay
func (pr *PrivateRelay) GetAllEvents() []*nostr.Event {
	pr.mutex.RLock()
	defer pr.mutex.RUnlock()

	events := make([]*nostr.Event, 0, len(pr.store))
	for _, event := range pr.store {
		events = append(events, event)
	}
	return events
}
