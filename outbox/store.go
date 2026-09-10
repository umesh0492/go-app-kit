package outbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrLeaseExpired is returned when updating an outbox event whose lease has expired
// or been acquired by another worker.
var ErrLeaseExpired = errors.New("outbox: lease expired or acquired by another worker")

// DBOperator abstracts PostgreSQL operations across connection pools and transactions.
type DBOperator interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Dialect specifies the database SQL dialect (PostgreSQL native via pgx).
type Dialect string

const (
	DialectPostgres Dialect = "postgres"
)

// StoreOption configures pgStore behavior.
type StoreOption func(*pgStore)

// WithDialect specifies the database dialect (DialectPostgres).
func WithDialect(d Dialect) StoreOption {
	return func(s *pgStore) {
		s.dialect = d
	}
}

// WithTableName specifies a custom table name for outbox events (default: "outbox_events").
func WithTableName(name string) StoreOption {
	return func(s *pgStore) {
		if name != "" {
			s.tableName = name
		}
	}
}

// WithLeaseDuration configures the worker lease lock duration (default: 60s, minimum: 5s).
func WithLeaseDuration(d time.Duration) StoreOption {
	return func(s *pgStore) {
		if d >= 5*time.Second {
			s.leaseDuration = d
		} else if d > 0 {
			s.leaseDuration = 5 * time.Second
		}
	}
}

// Store defines persistence operations for outbox events.
type Store interface {
	Insert(ctx context.Context, op DBOperator, event Event) error
	FetchPendingBatch(ctx context.Context, limit int) ([]Event, error)
	MarkPublished(ctx context.Context, id uuid.UUID, leaseToken ...uuid.UUID) error
	MarkFailed(ctx context.Context, id uuid.UUID, lastErr string, nextRetry time.Time, finalFail bool, leaseToken ...uuid.UUID) error
}

type pgStore struct {
	db            DBOperator
	dialect       Dialect
	tableName     string
	leaseDuration time.Duration
}

// NewPGStore creates a PostgreSQL-backed outbox store with optional configuration.
func NewPGStore(db DBOperator, opts ...StoreOption) Store {
	s := &pgStore{
		db:            db,
		dialect:       DialectPostgres,
		tableName:     "outbox_events",
		leaseDuration: 60 * time.Second,
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.leaseDuration < 5*time.Second {
		s.leaseDuration = 60 * time.Second
	}
	return s
}

// Insert inserts an outbox event using the given transaction or connection operator.
func (s *pgStore) Insert(ctx context.Context, op DBOperator, event Event) error {
	if op == nil {
		op = s.db
	}

	scheduledAt := event.ScheduledAt
	if scheduledAt.IsZero() {
		scheduledAt = time.Now().UTC()
	}
	nextRetryAt := event.NextRetryAt
	if nextRetryAt.IsZero() {
		nextRetryAt = scheduledAt
	}

	query := fmt.Sprintf(`
		INSERT INTO %s (
			id, aggregate_type, aggregate_id, event_type, payload, status, 
			retry_count, max_retries, scheduled_at, next_retry_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, s.tableName)
	_, err := op.Exec(ctx, query,
		event.ID,
		event.AggregateType,
		event.AggregateID,
		event.EventType,
		event.Payload,
		event.Status,
		event.RetryCount,
		event.MaxRetries,
		scheduledAt,
		nextRetryAt,
		event.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert outbox event: %w", err)
	}
	return nil
}

// FetchPendingQuery returns the SQL query string used to fetch and atomically lease pending events for relay
// utilizing row-level locking (FOR UPDATE SKIP LOCKED), fencing tokens, and an atomic CTE leasing state machine.
func (s *pgStore) FetchPendingQuery() string {
	tbl := s.tableName
	if tbl == "" {
		tbl = "outbox_events"
	}

	leaseSecs := int(s.leaseDuration.Seconds())
	if leaseSecs <= 0 {
		leaseSecs = 60
	}

	return fmt.Sprintf(`WITH pending AS (
    SELECT id FROM %s
    WHERE (status = 'PENDING' OR (status = 'PROCESSING' AND locked_until < NOW()))
      AND next_retry_at <= NOW()
    ORDER BY created_at ASC
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
UPDATE %s
SET status = 'PROCESSING', locked_until = NOW() + INTERVAL '%ds', lease_token = gen_random_uuid()
WHERE id IN (SELECT id FROM pending)
RETURNING id, aggregate_type, aggregate_id, event_type, payload, retry_count, max_retries, status, locked_until, created_at, published_at, last_error, lease_token;`, tbl, tbl, leaseSecs)
}

// FetchPendingBatch queries and atomically leases ready-to-process events using SKIP LOCKED row-level locking.
func (s *pgStore) FetchPendingBatch(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 50
	}

	query := s.FetchPendingQuery()
	rows, err := s.db.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pending outbox events: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var e Event
		var lastErr *string
		var publishedAt *time.Time
		var lockedUntil *time.Time
		var leaseToken *uuid.UUID

		err := rows.Scan(
			&e.ID,
			&e.AggregateType,
			&e.AggregateID,
			&e.EventType,
			&e.Payload,
			&e.RetryCount,
			&e.MaxRetries,
			&e.Status,
			&lockedUntil,
			&e.CreatedAt,
			&publishedAt,
			&lastErr,
			&leaseToken,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan outbox event: %w", err)
		}
		if lastErr != nil {
			e.LastError = *lastErr
		}
		e.PublishedAt = publishedAt
		e.LockedUntil = lockedUntil
		e.LeaseToken = leaseToken
		events = append(events, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return events, nil
}

// MarkPublished marks the event as successfully processed. If a lease token is provided,
// it validates that the caller holds the active lease (strict fencing).
func (s *pgStore) MarkPublished(ctx context.Context, id uuid.UUID, leaseToken ...uuid.UUID) error {
	var query string
	var args []any

	if len(leaseToken) > 0 && leaseToken[0] != uuid.Nil {
		query = fmt.Sprintf(`
			UPDATE %s
			SET status = 'PUBLISHED', published_at = NOW(), locked_until = NULL, lease_token = NULL, last_error = NULL
			WHERE id = $1 AND lease_token = $2
		`, s.tableName)
		args = []any{id, leaseToken[0]}
	} else {
		query = fmt.Sprintf(`
			UPDATE %s
			SET status = 'PUBLISHED', published_at = NOW(), locked_until = NULL, lease_token = NULL, last_error = NULL
			WHERE id = $1
		`, s.tableName)
		args = []any{id}
	}

	tag, err := s.db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to mark outbox event as published: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseExpired
	}
	return nil
}

// MarkFailed updates the event with failure status, increments retry count, or sets to StatusDeadLetter.
// If a lease token is provided, it validates that the caller still holds the active lease (strict fencing).
func (s *pgStore) MarkFailed(ctx context.Context, id uuid.UUID, lastErr string, nextRetry time.Time, finalFail bool, leaseToken ...uuid.UUID) error {
	newStatus := StatusPending
	if finalFail {
		newStatus = StatusDeadLetter
	}

	var query string
	var args []any

	if len(leaseToken) > 0 && leaseToken[0] != uuid.Nil {
		query = fmt.Sprintf(`
			UPDATE %s
			SET status = $1, 
			    retry_count = retry_count + 1, 
			    last_error = $2, 
			    scheduled_at = $3,
			    next_retry_at = $3,
			    locked_until = NULL,
			    lease_token = NULL
			WHERE id = $4 AND lease_token = $5
		`, s.tableName)
		args = []any{newStatus, lastErr, nextRetry, id, leaseToken[0]}
	} else {
		query = fmt.Sprintf(`
			UPDATE %s
			SET status = $1, 
			    retry_count = retry_count + 1, 
			    last_error = $2, 
			    scheduled_at = $3,
			    next_retry_at = $3,
			    locked_until = NULL,
			    lease_token = NULL
			WHERE id = $4
		`, s.tableName)
		args = []any{newStatus, lastErr, nextRetry, id}
	}

	tag, err := s.db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update failed outbox event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseExpired
	}
	return nil
}
