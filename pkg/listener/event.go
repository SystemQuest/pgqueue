package listener

import (
	"encoding/json"
	"time"
)

// Operation represents the type of database operation
type Operation string

const (
	OperationInsert   Operation = "insert"
	OperationUpdate   Operation = "update"
	OperationDelete   Operation = "delete"
	OperationTruncate Operation = "truncate"
)

// Event represents a PostgreSQL notification event
type Event struct {
	Channel   string    `json:"channel"`
	Operation Operation `json:"operation"`
	SentAt    time.Time `json:"sent_at"`
	Table     string    `json:"table"`
	Received  time.Time `json:"-"` // Set when received, not part of JSON payload
}

// Latency calculates the time between when the event was sent and received
func (e *Event) Latency() time.Duration {
	if e.Received.IsZero() {
		return 0
	}
	return e.Received.Sub(e.SentAt)
}

// ParseEvent parses a JSON payload into an Event
func ParseEvent(payload string) (*Event, error) {
	var event Event
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return nil, err
	}

	// Set received timestamp
	event.Received = time.Now()

	return &event, nil
}
