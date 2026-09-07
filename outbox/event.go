package outbox

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Status indicates the current lifecycle state of an outbox event.
type Status string

const (
	StatusPending    Status = "PENDING"
	StatusProcessing Status = "PROCESSING"
	StatusPublished  Status = "PUBLISHED"
	StatusDeadLetter Status = "DEAD_LETTER"

	// Deprecated: StatusFailed is superseded by StatusDeadLetter for terminal failures.
	// Transient errors remain StatusPending with exponential backoff (NextRetryAt).
	StatusFailed Status = "FAILED"
)

// IsTerminal reports whether the status is a terminal lifecycle state.
func (s Status) IsTerminal() bool {
	return s == StatusPublished || s == StatusDeadLetter || s == StatusFailed
}

// Event models a domain event transactionally written alongside business state mutations.
type Event struct {
	ID            uuid.UUID  `json:"id"`
	AggregateType string     `json:"aggregate_type"`
	AggregateID   string     `json:"aggregate_id"`
	EventType     string     `json:"event_type"`
	Payload       []byte     `json:"payload"`
	Status        Status     `json:"status"`
	RetryCount    int        `json:"retry_count"`
	MaxRetries    int        `json:"max_retries"`
	LastError     string     `json:"last_error,omitempty"`
	ScheduledAt   time.Time  `json:"scheduled_at"`
	NextRetryAt   time.Time  `json:"next_retry_at,omitempty"`
	LockedUntil   *time.Time `json:"locked_until,omitempty"`
	LeaseToken    *uuid.UUID `json:"lease_token,omitempty"`
	PublishedAt   *time.Time `json:"published_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// NewEvent constructs a new Event with a unique UUID, PENDING status, and JSON-encoded payload.
func NewEvent(aggregateType, aggregateID, eventType string, payload any, maxRetries ...int) (*Event, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize event payload: %w", err)
	}

	retries := 5
	if len(maxRetries) > 0 && maxRetries[0] >= 0 {
		retries = maxRetries[0]
	}

	now := time.Now().UTC()
	return &Event{
		ID:            uuid.New(),
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
		EventType:     eventType,
		Payload:       data,
		Status:        StatusPending,
		RetryCount:    0,
		MaxRetries:    retries,
		ScheduledAt:   now,
		NextRetryAt:   now,
		CreatedAt:     now,
	}, nil
}

// UnmarshalPayload deserializes the event JSON payload into a destination target struct.
func (e *Event) UnmarshalPayload(target any) error {
	if err := json.Unmarshal(e.Payload, target); err != nil {
		return fmt.Errorf("failed to unmarshal event payload: %w", err)
	}
	return nil
}

// IsDeadLetter reports whether the event has entered the dead-letter queue terminal status.
func (e *Event) IsDeadLetter() bool {
	return e.Status == StatusDeadLetter
}

// MarkDeadLetter transitions the event directly to StatusDeadLetter with an explanatory reason.
func (e *Event) MarkDeadLetter(reason string) {
	e.Status = StatusDeadLetter
	e.LastError = reason
}
