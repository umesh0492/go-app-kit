package audit_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/umesh0492/go-app-kit/audit"
)

type mockDBOperator struct {
	mu           sync.Mutex
	executedSQL  []string
	executedArgs [][]any
	execErr      error
	execHook     func(ctx context.Context, sql string, args []any)
}

func (m *mockDBOperator) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if m.execHook != nil {
		m.execHook(ctx, sql, args)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.execErr != nil {
		return pgconn.CommandTag{}, m.execErr
	}
	m.executedSQL = append(m.executedSQL, sql)
	m.executedArgs = append(m.executedArgs, args)
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func TestComputeDiff(t *testing.T) {
	before := map[string]any{
		"status": "DRAFT",
		"amount": 100.0,
		"tags":   "billing",
	}
	after := map[string]any{
		"status": "ISSUED", // modified
		"amount": 100.0,    // unchanged
		"notes":  "urgent", // added
		// tags removed
	}

	diff := audit.ComputeDiff(before, after)

	if len(diff) != 3 {
		t.Fatalf("expected 3 diff entries, got %d", len(diff))
	}

	// Modified field
	statusDiff, ok := diff["status"]
	if !ok || statusDiff.Old != "DRAFT" || statusDiff.New != "ISSUED" {
		t.Fatalf("unexpected status diff: %+v", statusDiff)
	}

	// Added field
	notesDiff, ok := diff["notes"]
	if !ok || notesDiff.Old != nil || notesDiff.New != "urgent" {
		t.Fatalf("unexpected notes diff: %+v", notesDiff)
	}

	// Removed field
	tagsDiff, ok := diff["tags"]
	if !ok || tagsDiff.Old != "billing" || tagsDiff.New != nil {
		t.Fatalf("unexpected tags diff: %+v", tagsDiff)
	}

	// Unchanged field should not appear
	if _, ok := diff["amount"]; ok {
		t.Fatalf("unchanged field 'amount' should not be in diff")
	}
}

func TestNewEvent_WithContext(t *testing.T) {
	actor := audit.Actor{
		ID:        "usr-100",
		Email:     "finance@acme.com",
		Role:      "FinanceManager",
		IPAddress: "192.168.1.10",
		UserAgent: "Mozilla/5.0",
	}

	ctx := context.Background()
	ctx = audit.ContextWithActor(ctx, actor)
	ctx = audit.WithComment(ctx, "Approved per board approval #42")

	retrievedActor, ok := audit.ActorFromContext(ctx)
	if !ok || retrievedActor.ID != actor.ID {
		t.Fatalf("ActorFromContext failed: %+v", retrievedActor)
	}

	before := map[string]any{"status": "PENDING"}
	after := map[string]any{"status": "APPROVED"}

	event := audit.NewEvent(ctx, "APPROVE_INVOICE", "Invoice", "INV-2026-001", before, after)

	if event.Action != "APPROVE_INVOICE" || event.EntityType != "Invoice" || event.EntityID != "INV-2026-001" {
		t.Fatalf("unexpected event fields: %+v", event)
	}
	if event.Actor.ID != actor.ID || event.Actor.Email != actor.Email {
		t.Fatalf("unexpected actor: %+v", event.Actor)
	}
	if event.Comment != "Approved per board approval #42" {
		t.Fatalf("unexpected comment: %s", event.Comment)
	}
	if len(event.Diff) != 1 {
		t.Fatalf("expected 1 diff field, got %d", len(event.Diff))
	}
}

func TestPGRecorder_Record_Success(t *testing.T) {
	mockDB := &mockDBOperator{}
	recorder, err := audit.NewPGRecorder(audit.Config{
		DB: mockDB,
	})
	if err != nil {
		t.Fatalf("NewPGRecorder failed: %v", err)
	}
	defer recorder.Close()

	evt := audit.Event{
		Action:      "UPDATE_RATE",
		EntityType:  "PriceTable",
		EntityID:    "RATE-01",
		BeforeState: map[string]any{"rate": 10},
		AfterState:  map[string]any{"rate": 15},
		Diff:        map[string]audit.FieldDiff{"rate": {Old: 10, New: 15}},
		Metadata:    map[string]string{"source": "cli"},
	}

	err = recorder.Record(context.Background(), evt)
	if err != nil {
		t.Fatalf("Record failed: %v", err)
	}

	mockDB.mu.Lock()
	count := len(mockDB.executedSQL)
	mockDB.mu.Unlock()

	if count != 1 {
		t.Fatalf("expected 1 SQL execution, got %d", count)
	}
}

func TestPGRecorder_RecordAsync(t *testing.T) {
	mockDB := &mockDBOperator{}
	recorder, err := audit.NewPGRecorder(audit.Config{
		DB:        mockDB,
		Workers:   1,
		QueueSize: 10,
	})
	if err != nil {
		t.Fatalf("NewPGRecorder failed: %v", err)
	}

	evt := audit.Event{
		Action:     "ASYNC_LOG",
		EntityType: "Order",
		EntityID:   "ORD-99",
	}

	err = recorder.RecordAsync(evt)
	if err != nil {
		t.Fatalf("RecordAsync failed: %v", err)
	}

	// Close waits for all workers to finish
	recorder.Close()

	mockDB.mu.Lock()
	count := len(mockDB.executedSQL)
	mockDB.mu.Unlock()

	if count != 1 {
		t.Fatalf("expected async task to execute, got %d queries", count)
	}
}

func TestPGRecorder_Validation(t *testing.T) {
	_, err := audit.NewPGRecorder(audit.Config{})
	if !errors.Is(err, audit.ErrMissingDB) {
		t.Fatalf("expected ErrMissingDB, got %v", err)
	}

	mockDB := &mockDBOperator{
		execErr: errors.New("db connection failure"),
	}
	recorder, _ := audit.NewPGRecorder(audit.Config{DB: mockDB})
	defer recorder.Close()

	err = recorder.Record(context.Background(), audit.Event{})
	if err == nil {
		t.Fatalf("expected DB error on Record")
	}
}

func TestWithComment_And_Comment(t *testing.T) {
	var nilCtx context.Context
	if got := audit.Comment(nilCtx); got != "" {
		t.Fatalf("expected empty comment for nil context, got %q", got)
	}
	if got := audit.Comment(context.Background()); got != "" {
		t.Fatalf("expected empty comment when not set, got %q", got)
	}

	ctx := audit.WithComment(context.Background(), "first comment")
	if got := audit.Comment(ctx); got != "first comment" {
		t.Fatalf("expected 'first comment', got %q", got)
	}

	ctx2 := audit.WithComment(ctx, "second comment")
	if got := audit.Comment(ctx2); got != "second comment" {
		t.Fatalf("expected 'second comment', got %q", got)
	}

	// Sibling contexts isolation
	parent := context.Background()
	sib1 := audit.WithComment(parent, "sib1")
	sib2 := audit.WithComment(parent, "sib2")
	if audit.Comment(sib1) != "sib1" || audit.Comment(sib2) != "sib2" || audit.Comment(parent) != "" {
		t.Fatalf("context isolation failed")
	}
}

func TestPGRecorder_RecordAsync_QueueFullPolicy(t *testing.T) {
	t.Run("PolicyFallbackSync persists synchronously on full queue", func(t *testing.T) {
		blockCh := make(chan struct{})
		mockDB := &mockDBOperator{
			execHook: func(ctx context.Context, sql string, args []any) {
				if len(args) > 6 && args[6] == "BLOCKING_EVT" {
					<-blockCh
				}
			},
		}

		// 1 worker, 1 queue slot
		recorder, err := audit.NewPGRecorder(audit.Config{
			DB:              mockDB,
			Workers:         1,
			QueueSize:       1,
			QueueFullPolicy: audit.PolicyFallbackSync,
		})
		if err != nil {
			t.Fatalf("NewPGRecorder failed: %v", err)
		}
		defer recorder.Close()

		// 1. Submit BLOCKING_EVT: picked up by the 1 worker, blocks in execHook on blockCh
		_ = recorder.RecordAsync(audit.Event{Action: "BLOCKING_EVT", EntityType: "Test", EntityID: "1"})
		// Give worker goroutine a brief moment to pick up the task
		time.Sleep(10 * time.Millisecond)

		// 2. Submit QUEUED_EVT: sits in the 1-capacity queue
		_ = recorder.RecordAsync(audit.Event{Action: "QUEUED_EVT", EntityType: "Test", EntityID: "2"})

		// 3. Submit FALLBACK_SYNC_LOG: queue is full, triggers PolicyFallbackSync synchronously
		fallbackEvt := audit.Event{
			Action:     "FALLBACK_SYNC_LOG",
			EntityType: "Order",
			EntityID:   "ORD-FALLBACK",
		}
		err = recorder.RecordAsync(fallbackEvt)
		if err != nil {
			t.Fatalf("expected RecordAsync with PolicyFallbackSync to succeed, got: %v", err)
		}

		// Unblock the worker so clean drain occurs
		close(blockCh)
		recorder.Close()

		mockDB.mu.Lock()
		defer mockDB.mu.Unlock()
		found := false
		for _, args := range mockDB.executedArgs {
			if len(args) > 6 && args[6] == "FALLBACK_SYNC_LOG" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected fallback synchronous log to be persisted in database")
		}
	})

	t.Run("PolicyDrop returns ErrQueueFull when queue is full", func(t *testing.T) {
		blockCh := make(chan struct{})
		mockDB := &mockDBOperator{
			execHook: func(ctx context.Context, sql string, args []any) {
				if len(args) > 6 && args[6] == "BLOCKING_EVT" {
					<-blockCh
				}
			},
		}

		recorder, err := audit.NewPGRecorder(audit.Config{
			DB:              mockDB,
			Workers:         1,
			QueueSize:       1,
			QueueFullPolicy: audit.PolicyDrop,
		})
		if err != nil {
			t.Fatalf("NewPGRecorder failed: %v", err)
		}
		defer recorder.Close()

		_ = recorder.RecordAsync(audit.Event{Action: "BLOCKING_EVT", EntityType: "Test", EntityID: "1"})
		time.Sleep(10 * time.Millisecond)
		_ = recorder.RecordAsync(audit.Event{Action: "QUEUED_EVT", EntityType: "Test", EntityID: "2"})

		dropEvt := audit.Event{Action: "DROP_LOG", EntityType: "Order", EntityID: "ORD-DROP"}
		err = recorder.RecordAsync(dropEvt)
		if err == nil {
			t.Fatalf("expected error from PolicyDrop on full queue, got nil")
		}

		close(blockCh)
		recorder.Close()
	})
}

func TestWithTableName_SanitizationAndExecution(t *testing.T) {
	mockDB := &mockDBOperator{}

	// 1. Valid table names with Option
	validNames := []string{
		"audit_logs",
		"tenant_audit_records",
		"_private_audit",
		"public.audit_logs",
		"compliance_schema.audit_events_2026",
	}

	for _, name := range validNames {
		recorder, err := audit.NewPGRecorder(audit.Config{DB: mockDB}, audit.WithTableName(name))
		if err != nil {
			t.Fatalf("expected valid table name %q to succeed, got: %v", name, err)
		}
		recorder.Close()
	}

	// 2. Execution with custom table name
	customRecorder, err := audit.NewPGRecorder(audit.Config{DB: mockDB}, audit.WithTableName("app_tenant.audit_trail"))
	if err != nil {
		t.Fatalf("failed to create custom recorder: %v", err)
	}
	defer customRecorder.Close()

	evt := audit.Event{Action: "TEST_CUSTOM_TABLE", EntityType: "User", EntityID: "U1"}
	err = customRecorder.Record(context.Background(), evt)
	if err != nil {
		t.Fatalf("Record failed: %v", err)
	}

	mockDB.mu.Lock()
	lastSQL := mockDB.executedSQL[len(mockDB.executedSQL)-1]
	mockDB.mu.Unlock()

	if !strings.Contains(lastSQL, "INSERT INTO app_tenant.audit_trail (") {
		t.Fatalf("expected SQL to contain custom table name, got: %s", lastSQL)
	}

	// 3. Invalid table names (SQL injection attempts, invalid identifiers)
	invalidNames := []string{
		"audit_logs; DROP TABLE users; --",
		"audit logs",
		"123_audit",
		"audit-table",
		"schema.table.extra",
		"audit(id, action)",
		"table--comment",
		"schema..table",
		"table'name",
		"table\"name",
	}

	for _, badName := range invalidNames {
		_, err := audit.NewPGRecorder(audit.Config{DB: mockDB}, audit.WithTableName(badName))
		if !errors.Is(err, audit.ErrInvalidTableName) {
			t.Fatalf("expected ErrInvalidTableName for %q, got: %v", badName, err)
		}

		// Also check via Config.TableName
		_, err = audit.NewPGRecorder(audit.Config{
			DB:        mockDB,
			TableName: badName,
		})
		if !errors.Is(err, audit.ErrInvalidTableName) {
			t.Fatalf("expected ErrInvalidTableName via Config for %q, got: %v", badName, err)
		}
	}
}
