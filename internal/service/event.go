package service

import (
	"sync"
	"time"
)

type EventType string

const (
	EventArtworkDiscovered EventType = "artwork_discovered"
	EventTaskStarted       EventType = "task_started"
	EventTaskCompleted     EventType = "task_completed"
	EventTaskFailed        EventType = "task_failed"
	EventSyncFinished      EventType = "sync_finished"
)

type Event struct {
	Type      EventType `json:"type"`
	Timestamp int64     `json:"timestamp"`
	Data      any       `json:"data"`
}

type EventHub struct {
	mu      sync.Mutex
	clients map[chan Event]bool
}

func NewEventHub() *EventHub {
	return &EventHub{
		clients: make(map[chan Event]bool),
	}
}

func (h *EventHub) Subscribe() chan Event {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch := make(chan Event, 256)
	h.clients[ch] = true
	return ch
}

func (h *EventHub) Unsubscribe(ch chan Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.clients[ch]; ok {
		delete(h.clients, ch)
		close(ch)
	}
}

func (h *EventHub) Publish(eventType EventType, data any) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ev := Event{
		Type:      eventType,
		Timestamp: time.Now().UnixMilli(),
		Data:      data,
	}

	for ch := range h.clients {
		select {
		case ch <- ev:
		default:
			// client channel full, skip or drop to avoid blocking
		}
	}
}
