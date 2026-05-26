package livebus

import (
	"sync"
	"time"
)

type Event struct {
	Type      string    `json:"type"`
	Entity    string    `json:"entity"`
	EntityID  string    `json:"entityId,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Payload   any       `json:"payload,omitempty"`
}

type Bus struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

func New() *Bus {
	return &Bus{
		subs: make(map[chan Event]struct{}),
	}
}

func NewEvent(eventType, entity, entityID string, payload any) Event {
	return Event{
		Type:      eventType,
		Entity:    entity,
		EntityID:  entityID,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}

func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)

	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	return ch, func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
}

func (b *Bus) Publish(event Event) {
	if b == nil {
		return
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	for ch := range b.subs {
		select {
		case ch <- event:
		default:
			// Buffer full: discard the oldest event to make room, then send a
			// missed_events notification so the subscriber knows to re-fetch.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- Event{
				Type:      "missed_events",
				Entity:    event.Entity,
				Timestamp: time.Now().UTC(),
			}:
			default:
			}
		}
	}
}

func (b *Bus) Close() {
	if b == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	for ch := range b.subs {
		close(ch)
		delete(b.subs, ch)
	}
}
