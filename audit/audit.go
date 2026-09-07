package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/umesh0492/go-libs/workerpool"
)

var (
	ErrMissingDB        = errors.New("database operator is required for audit recording")
	ErrInvalidTableName = errors.New("invalid table name")
)

var tableNameRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*(\.[a-zA-Z_][a-zA-Z0-9_]*)?$`)

type contextKey struct{}
type commentContextKey struct{}

// DBOperator defines minimal database execution needed to persist audit records.
type DBOperator interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Actor captures the identity and network provenance of the person or system performing an action.
type Actor struct {
	ID        string `json:"id"`
	Email     string `json:"email,omitempty"`
	Role      string `json:"role,omitempty"`
	IPAddress string `json:"ip_address,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
}

// ContextWithActor attaches actor details to the request context.
func ContextWithActor(ctx context.Context, actor Actor) context.Context {
	return context.WithValue(ctx, contextKey{}, actor)
}

// ActorFromContext extracts actor details from the context if available.
func ActorFromContext(ctx context.Context) (Actor, bool) {
	actor, ok := ctx.Value(contextKey{}).(Actor)
	return actor, ok
}

// WithComment attaches an operational justification comment to the request context.
// Audit log recorders automatically extract this comment to populate audit_logs.comment.
func WithComment(ctx context.Context, comment string) context.Context {
	return context.WithValue(ctx, commentContextKey{}, comment)
}

// Comment extracts the operational justification comment from ctx, or returns empty string if not set.
func Comment(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, ok := ctx.Value(commentContextKey{}).(string)
	if !ok {
		return ""
	}
	return v
}

// FieldDiff represents a single property change between before and after states.
type FieldDiff struct {
	Old any `json:"old"`
	New any `json:"new"`
}

// Event models an immutable compliance audit record.
type Event struct {
	ID          uuid.UUID            `json:"id"`
	Actor       Actor                `json:"actor"`
	Action      string               `json:"action"`
	EntityType  string               `json:"entity_type"`
	EntityID    string               `json:"entity_id"`
	BeforeState map[string]any       `json:"before_state,omitempty"`
	AfterState  map[string]any       `json:"after_state,omitempty"`
	Diff        map[string]FieldDiff `json:"diff,omitempty"`
	Comment     string               `json:"comment,omitempty"`
	Metadata    map[string]string    `json:"metadata,omitempty"`
	CreatedAt   time.Time            `json:"created_at"`
}

// ComputeDiff calculates structural property differences between before and after states.
func ComputeDiff(before, after map[string]any) map[string]FieldDiff {
	diff := make(map[string]FieldDiff)

	// Check modified and removed fields
	for k, oldVal := range before {
		newVal, exists := after[k]
		if !exists {
			diff[k] = FieldDiff{Old: oldVal, New: nil}
			continue
		}
		if !reflect.DeepEqual(oldVal, newVal) {
			diff[k] = FieldDiff{Old: oldVal, New: newVal}
		}
	}

	// Check newly added fields
	for k, newVal := range after {
		if _, exists := before[k]; !exists {
			diff[k] = FieldDiff{Old: nil, New: newVal}
		}
	}

	return diff
}

// NewEvent initializes an Event, automatically resolving diffs, context comments, and timestamps.
func NewEvent(ctx context.Context, action, entityType, entityID string, before, after map[string]any) Event {
	actor, _ := ActorFromContext(ctx)
	comment := Comment(ctx)

	var diff map[string]FieldDiff
	if before != nil || after != nil {
		diff = ComputeDiff(before, after)
	}

	return Event{
		ID:          uuid.New(),
		Actor:       actor,
		Action:      action,
		EntityType:  entityType,
		EntityID:    entityID,
		BeforeState: before,
		AfterState:  after,
		Diff:        diff,
		Comment:     comment,
		Metadata:    make(map[string]string),
		CreatedAt:   time.Now().UTC(),
	}
}

// Recorder defines methods to record audit trails synchronously or asynchronously.
type Recorder interface {
	Record(ctx context.Context, event Event) error
	RecordAsync(event Event) error
	Close()
}

// QueueFullPolicy defines the behavior when the asynchronous audit logging queue is full.
type QueueFullPolicy string

const (
	// PolicyFallbackSync synchronously persists the audit log to the database if the queue is full.
	// This guarantees zero loss of audit trails under load spikes (recommended for SOC2/ISO27001).
	PolicyFallbackSync QueueFullPolicy = "FALLBACK_SYNC"

	// PolicyBlock blocks the caller until queue capacity is available or timeout occurs.
	PolicyBlock QueueFullPolicy = "BLOCK"

	// PolicyDrop drops the event and logs a warning when the queue is full.
	PolicyDrop QueueFullPolicy = "DROP"
)

// Option configures pgRecorder behavior.
type Option func(*pgRecorder)

// WithTableName specifies a custom table name for audit logs (default: "audit_logs").
// It validates the table name against a strict regex identifier ('^[a-zA-Z_][a-zA-Z0-9_]*(\.[a-zA-Z_][a-zA-Z0-9_]*)?$')
// before fmt.Sprintf to prevent SQL injection.
func WithTableName(name string) Option {
	return func(r *pgRecorder) {
		if !tableNameRegex.MatchString(name) {
			r.initErr = fmt.Errorf("%w: %q does not match identifier pattern '^[a-zA-Z_][a-zA-Z0-9_]*(\\.[a-zA-Z_][a-zA-Z0-9_]*)?$'", ErrInvalidTableName, name)
			return
		}
		r.tableName = name
	}
}

// Config configures the PostgreSQL audit recorder.
type Config struct {
	DB              DBOperator
	Workers         int
	QueueSize       int
	Logger          *slog.Logger
	QueueFullPolicy QueueFullPolicy // default: PolicyFallbackSync
	TableName       string          // optional custom table name (default: "audit_logs")
}

type pgRecorder struct {
	db              DBOperator
	pool            *workerpool.Pool
	logger          *slog.Logger
	queueFullPolicy QueueFullPolicy
	tableName       string
	insertQuery     string
	initErr         error
}

// TableName returns the configured table name for the audit recorder.
func (r *pgRecorder) TableName() string {
	return r.tableName
}

// InsertQuery returns the prepared SQL INSERT statement.
func (r *pgRecorder) InsertQuery() string {
	return r.insertQuery
}

// NewPGRecorder initializes an audit recorder backed by PostgreSQL and a bounded workerpool.
func NewPGRecorder(cfg Config, opts ...Option) (Recorder, error) {
	if cfg.DB == nil {
		return nil, ErrMissingDB
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 200
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	policy := cfg.QueueFullPolicy
	if policy == "" {
		policy = PolicyFallbackSync
	}

	tableName := cfg.TableName
	if tableName == "" {
		tableName = "audit_logs"
	} else if !tableNameRegex.MatchString(tableName) {
		return nil, fmt.Errorf("%w: %q does not match identifier pattern '^[a-zA-Z_][a-zA-Z0-9_]*(\\.[a-zA-Z_][a-zA-Z0-9_]*)?$'", ErrInvalidTableName, tableName)
	}

	p := workerpool.New(cfg.Workers, cfg.QueueSize, workerpool.WithLogger(cfg.Logger))

	r := &pgRecorder{
		db:              cfg.DB,
		pool:            p,
		logger:          cfg.Logger,
		queueFullPolicy: policy,
		tableName:       tableName,
	}

	for _, opt := range opts {
		opt(r)
	}
	if r.initErr != nil {
		return nil, r.initErr
	}

	// Validate r.tableName before fmt.Sprintf to prevent SQL injection
	if !tableNameRegex.MatchString(r.tableName) {
		return nil, fmt.Errorf("%w: %q does not match identifier pattern '^[a-zA-Z_][a-zA-Z0-9_]*(\\.[a-zA-Z_][a-zA-Z0-9_]*)?$'", ErrInvalidTableName, r.tableName)
	}

	r.insertQuery = fmt.Sprintf(`
		INSERT INTO %s (
			id, actor_id, actor_email, actor_role, ip_address, user_agent,
			action, entity_type, entity_id, before_state, after_state, diff,
			comment, metadata, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11, $12,
			$13, $14, $15
		)
	`, r.tableName)

	return r, nil
}

// Record synchronously inserts an audit record into PostgreSQL.
func (r *pgRecorder) Record(ctx context.Context, event Event) error {
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}

	var beforeJSON, afterJSON, diffJSON, metaJSON []byte
	var err error

	if event.BeforeState != nil {
		if beforeJSON, err = json.Marshal(event.BeforeState); err != nil {
			return fmt.Errorf("failed to marshal before_state: %w", err)
		}
	}
	if event.AfterState != nil {
		if afterJSON, err = json.Marshal(event.AfterState); err != nil {
			return fmt.Errorf("failed to marshal after_state: %w", err)
		}
	}
	if event.Diff != nil {
		if diffJSON, err = json.Marshal(event.Diff); err != nil {
			return fmt.Errorf("failed to marshal diff: %w", err)
		}
	}
	if event.Metadata != nil {
		if metaJSON, err = json.Marshal(event.Metadata); err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}
	}

	query := r.insertQuery
	if query == "" {
		query = fmt.Sprintf(`
			INSERT INTO %s (
				id, actor_id, actor_email, actor_role, ip_address, user_agent,
				action, entity_type, entity_id, before_state, after_state, diff,
				comment, metadata, created_at
			) VALUES (
				$1, $2, $3, $4, $5, $6,
				$7, $8, $9, $10, $11, $12,
				$13, $14, $15
			)
		`, r.tableName)
	}

	_, err = r.db.Exec(ctx, query,
		event.ID,
		event.Actor.ID,
		event.Actor.Email,
		event.Actor.Role,
		event.Actor.IPAddress,
		event.Actor.UserAgent,
		event.Action,
		event.EntityType,
		event.EntityID,
		beforeJSON,
		afterJSON,
		diffJSON,
		event.Comment,
		metaJSON,
		event.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert audit log: %w", err)
	}

	return nil
}

// RecordAsync dispatches an audit record into the workerpool to prevent latency impact on user operations.
// If the queue is full, behavior is determined by QueueFullPolicy (defaults to PolicyFallbackSync).
func (r *pgRecorder) RecordAsync(event Event) error {
	task := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := r.Record(ctx, event); err != nil {
			r.logger.Error("failed to record async audit log",
				"id", event.ID,
				"action", event.Action,
				"entity_type", event.EntityType,
				"error", err,
			)
		}
	}

	err := r.pool.Submit(task)
	if err == nil {
		return nil
	}

	if errors.Is(err, workerpool.ErrQueueFull) {
		switch r.queueFullPolicy {
		case PolicyFallbackSync:
			r.logger.Warn("audit async queue full; falling back to synchronous database persist",
				"id", event.ID,
				"action", event.Action,
				"entity_type", event.EntityType,
			)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return r.Record(ctx, event)
		case PolicyBlock:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return r.pool.SubmitContext(ctx, task)
		case PolicyDrop:
			r.logger.Warn("audit async queue full; dropping audit log event",
				"id", event.ID,
				"action", event.Action,
				"entity_type", event.EntityType,
			)
			return err
		}
	}

	return err
}

// Close gracefully drains and stops the worker pool.
func (r *pgRecorder) Close() {
	r.pool.StopWait()
}
