package outbox_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/umesh0492/go-app-kit/outbox"
)

type mockStore struct {
	mu           sync.Mutex
	events       []outbox.Event
	publishedIDs []uuid.UUID
	failedEvents map[uuid.UUID]struct {
		err       string
		nextRetry time.Time
		finalFail bool
	}
	lockedEvents map[uuid.UUID]bool
	fetchErr     error
	markPubErr   error
	markFailErr  error
}

func newMockStore() *mockStore {
	return &mockStore{
		failedEvents: make(map[uuid.UUID]struct {
			err       string
			nextRetry time.Time
			finalFail bool
		}),
		lockedEvents: make(map[uuid.UUID]bool),
	}
}

func (m *mockStore) Insert(ctx context.Context, op outbox.DBOperator, event outbox.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	return nil
}

func (m *mockStore) FetchPendingBatch(ctx context.Context, limit int) ([]outbox.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fetchErr != nil {
		return nil, m.fetchErr
	}

	var batch []outbox.Event
	now := time.Now().UTC()
	for i := range m.events {
		if len(batch) >= limit {
			break
		}
		e := &m.events[i]
		if e.Status == outbox.StatusPublished || e.Status == outbox.StatusDeadLetter {
			continue
		}

		isPending := e.Status == outbox.StatusPending
		isExpiredProcessing := e.Status == outbox.StatusProcessing && (e.LockedUntil == nil || e.LockedUntil.Before(now))
		if (isPending || isExpiredProcessing) && !m.lockedEvents[e.ID] {
			m.lockedEvents[e.ID] = true
			leaseExpiry := now.Add(60 * time.Second)
			leaseTok := uuid.New()
			e.Status = outbox.StatusProcessing
			e.LockedUntil = &leaseExpiry
			e.LeaseToken = &leaseTok
			batch = append(batch, *e)
		}
	}
	return batch, nil
}

func (m *mockStore) MarkPublished(ctx context.Context, id uuid.UUID, leaseToken ...uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.markPubErr != nil {
		return m.markPubErr
	}
	delete(m.lockedEvents, id) // Release lock upon transaction commit
	m.publishedIDs = append(m.publishedIDs, id)
	for i, e := range m.events {
		if e.ID == id {
			if len(leaseToken) > 0 && leaseToken[0] != uuid.Nil {
				if e.LeaseToken == nil || *e.LeaseToken != leaseToken[0] {
					return outbox.ErrLeaseExpired
				}
			}
			now := time.Now().UTC()
			m.events[i].Status = outbox.StatusPublished
			m.events[i].PublishedAt = &now
		}
	}
	return nil
}

func (m *mockStore) MarkFailed(ctx context.Context, id uuid.UUID, lastErr string, nextRetry time.Time, finalFail bool, leaseToken ...uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.markFailErr != nil {
		return m.markFailErr
	}
	delete(m.lockedEvents, id) // Release lock upon failure recording
	m.failedEvents[id] = struct {
		err       string
		nextRetry time.Time
		finalFail bool
	}{
		err:       lastErr,
		nextRetry: nextRetry,
		finalFail: finalFail,
	}

	for i, e := range m.events {
		if e.ID == id {
			if len(leaseToken) > 0 && leaseToken[0] != uuid.Nil {
				if e.LeaseToken == nil || *e.LeaseToken != leaseToken[0] {
					return outbox.ErrLeaseExpired
				}
			}
			m.events[i].RetryCount++
			m.events[i].LastError = lastErr
			m.events[i].ScheduledAt = nextRetry
			m.events[i].NextRetryAt = nextRetry
			m.events[i].LockedUntil = nil
			m.events[i].LeaseToken = nil
			if finalFail {
				m.events[i].Status = outbox.StatusDeadLetter
			} else {
				m.events[i].Status = outbox.StatusPending
			}
		}
	}
	return nil
}

type mockDBOperator struct {
	execFunc  func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	queryFunc func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func (m *mockDBOperator) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if m.execFunc != nil {
		return m.execFunc(ctx, sql, args...)
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (m *mockDBOperator) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if m.queryFunc != nil {
		return m.queryFunc(ctx, sql, args...)
	}
	return nil, nil
}

func (m *mockDBOperator) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return nil
}

func TestEvent_NewAndUnmarshal(t *testing.T) {
	type OrderPayload struct {
		OrderID string  `json:"order_id"`
		Amount  float64 `json:"amount"`
	}

	payload := OrderPayload{OrderID: "ORD-123", Amount: 499.99}
	evt, err := outbox.NewEvent("Order", "ORD-123", "OrderCreated", payload, 3)
	if err != nil {
		t.Fatalf("NewEvent failed: %v", err)
	}

	if evt.ID == uuid.Nil {
		t.Fatalf("expected non-nil UUID")
	}
	if evt.Status != outbox.StatusPending {
		t.Fatalf("expected PENDING status, got %s", evt.Status)
	}
	if evt.MaxRetries != 3 {
		t.Fatalf("expected max retries 3, got %d", evt.MaxRetries)
	}

	var parsed OrderPayload
	if err := evt.UnmarshalPayload(&parsed); err != nil {
		t.Fatalf("UnmarshalPayload failed: %v", err)
	}
	if parsed.OrderID != "ORD-123" || parsed.Amount != 499.99 {
		t.Fatalf("unexpected unmarshaled data: %+v", parsed)
	}
}

func TestPGStore_InsertAndMark(t *testing.T) {
	var executedSQL []string
	mockOp := &mockDBOperator{
		execFunc: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			executedSQL = append(executedSQL, sql)
			return pgconn.NewCommandTag("UPDATE 1"), nil
		},
	}

	store := outbox.NewPGStore(mockOp)
	evt, _ := outbox.NewEvent("User", "usr-1", "UserRegistered", map[string]string{"name": "Alice"})

	err := store.Insert(context.Background(), mockOp, *evt)
	if err != nil {
		t.Fatalf("store.Insert failed: %v", err)
	}

	err = store.MarkPublished(context.Background(), evt.ID)
	if err != nil {
		t.Fatalf("store.MarkPublished failed: %v", err)
	}

	err = store.MarkFailed(context.Background(), evt.ID, "network error", time.Now().Add(5*time.Second), false)
	if err != nil {
		t.Fatalf("store.MarkFailed failed: %v", err)
	}

	err = store.MarkFailed(context.Background(), evt.ID, "fatal error", time.Now().Add(5*time.Second), true)
	if err != nil {
		t.Fatalf("store.MarkFailed final failed: %v", err)
	}

	if len(executedSQL) != 4 {
		t.Fatalf("expected 4 queries executed, got %d", len(executedSQL))
	}
}

func TestRelay_ProcessBatch_Success(t *testing.T) {
	store := newMockStore()
	evt1, _ := outbox.NewEvent("Invoice", "INV-1", "InvoiceCreated", map[string]string{"inv": "1"})
	evt2, _ := outbox.NewEvent("Invoice", "INV-2", "InvoiceCreated", map[string]string{"inv": "2"})
	_ = store.Insert(context.Background(), nil, *evt1)
	_ = store.Insert(context.Background(), nil, *evt2)

	var publishedEvents []outbox.Event
	publisher := outbox.PublisherFunc(func(ctx context.Context, event outbox.Event) error {
		publishedEvents = append(publishedEvents, event)
		return nil
	})

	relay, err := outbox.NewRelay(outbox.RelayConfig{
		Store:     store,
		Publisher: publisher,
		BatchSize: 10,
	})
	if err != nil {
		t.Fatalf("NewRelay failed: %v", err)
	}

	count, err := relay.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 processed events, got %d", count)
	}
	if len(publishedEvents) != 2 {
		t.Fatalf("expected 2 published events, got %d", len(publishedEvents))
	}
	if len(store.publishedIDs) != 2 {
		t.Fatalf("expected 2 marked published, got %d", len(store.publishedIDs))
	}
}

func TestRelay_ProcessBatch_RetryAndDeadLetter(t *testing.T) {
	store := newMockStore()
	// Event with 1 max retry so it fails permanently on 1st error
	evtFail, _ := outbox.NewEvent("Payment", "PAY-1", "PaymentFailed", map[string]string{"pay": "1"}, 1)
	// Event with 5 max retries so it enters retry backoff
	evtRetry, _ := outbox.NewEvent("Payment", "PAY-2", "PaymentPending", map[string]string{"pay": "2"}, 5)
	_ = store.Insert(context.Background(), nil, *evtFail)
	_ = store.Insert(context.Background(), nil, *evtRetry)

	publisher := outbox.PublisherFunc(func(ctx context.Context, event outbox.Event) error {
		return errors.New("upstream service unavailable")
	})

	relay, err := outbox.NewRelay(outbox.RelayConfig{
		Store:        store,
		Publisher:    publisher,
		BackoffBase:  100 * time.Millisecond,
		PollInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRelay failed: %v", err)
	}

	count, err := relay.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 processed, got %d", count)
	}

	failInfo := store.failedEvents[evtFail.ID]
	if !failInfo.finalFail {
		t.Fatalf("expected evtFail to be marked finalFail: %+v", failInfo)
	}
	if store.events[0].Status != outbox.StatusDeadLetter {
		t.Fatalf("expected evtFail status to be %s, got %s", outbox.StatusDeadLetter, store.events[0].Status)
	}

	retryInfo := store.failedEvents[evtRetry.ID]
	if retryInfo.finalFail {
		t.Fatalf("expected evtRetry NOT to be marked finalFail: %+v", retryInfo)
	}
	if store.events[1].Status != outbox.StatusPending {
		t.Fatalf("expected evtRetry status to remain %s, got %s", outbox.StatusPending, store.events[1].Status)
	}
}

func TestRelay_StartAndShutdown(t *testing.T) {
	store := newMockStore()
	publisher := outbox.PublisherFunc(func(ctx context.Context, event outbox.Event) error {
		return nil
	})

	relay, _ := outbox.NewRelay(outbox.RelayConfig{
		Store:        store,
		Publisher:    publisher,
		PollInterval: 10 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := relay.Start(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestRelay_ValidationErrors(t *testing.T) {
	_, err := outbox.NewRelay(outbox.RelayConfig{})
	if !errors.Is(err, outbox.ErrMissingStore) {
		t.Fatalf("expected ErrMissingStore, got %v", err)
	}

	_, err = outbox.NewRelay(outbox.RelayConfig{
		Store: newMockStore(),
	})
	if !errors.Is(err, outbox.ErrMissingPublisher) {
		t.Fatalf("expected ErrMissingPublisher, got %v", err)
	}
}

func TestBackoffWithJitter_BoundsAndRandomization(t *testing.T) {
	baseDelay := 50 * time.Millisecond
	maxDelay := 1 * time.Second

	// 1. Verify bounds across multiple retry attempts
	for attempt := 0; attempt <= 10; attempt++ {
		multiplier := int64(1) << attempt
		expectedCeiling := baseDelay * time.Duration(multiplier)
		if expectedCeiling > maxDelay {
			expectedCeiling = maxDelay
		}

		for sample := 0; sample < 20; sample++ {
			d := outbox.BackoffWithJitter(attempt, baseDelay, maxDelay)
			if d < 0 {
				t.Fatalf("attempt %d: negative delay %v", attempt, d)
			}
			if d > expectedCeiling {
				t.Fatalf("attempt %d: delay %v exceeded ceiling %v", attempt, d, expectedCeiling)
			}
		}
	}

	// 2. Verify randomization (jitter variance) to prevent thundering herd
	const samples = 100
	delays := make(map[time.Duration]bool)
	var minVal, maxVal time.Duration = maxDelay, 0

	for i := 0; i < samples; i++ {
		d := outbox.BackoffWithJitter(3, 100*time.Millisecond, 2*time.Second)
		delays[d] = true
		if d < minVal {
			minVal = d
		}
		if d > maxVal {
			maxVal = d
		}
	}

	// With 100 samples and 800ms ceiling, we expect high entropy (many distinct values)
	if len(delays) < 10 {
		t.Fatalf("expected randomized jitter across samples, got only %d distinct values", len(delays))
	}
	if minVal == maxVal {
		t.Fatalf("expected distinct min and max jitter values, got min=%v max=%v", minVal, maxVal)
	}

	// 3. Edge cases
	if d := outbox.BackoffWithJitter(-5, baseDelay, maxDelay); d < 0 || d > baseDelay {
		t.Fatalf("expected attempt < 0 to clamp to attempt 0, got %v", d)
	}
	if d := outbox.BackoffWithJitter(0, 0, maxDelay); d != 0 {
		t.Fatalf("expected baseDelay <= 0 to return 0, got %v", d)
	}
	if d := outbox.BackoffWithJitter(0, -10*time.Millisecond, maxDelay); d != 0 {
		t.Fatalf("expected negative baseDelay to return 0, got %v", d)
	}
	if d := outbox.BackoffWithJitter(5, 5*time.Second, 500*time.Millisecond); d > 500*time.Millisecond {
		t.Fatalf("expected cap when maxDelay < baseDelay, got %v", d)
	}
	// Very large attempt should not panic or overflow
	if d := outbox.BackoffWithJitter(100, baseDelay, maxDelay); d < 0 || d > maxDelay {
		t.Fatalf("expected large attempt to not overflow, got %v", d)
	}
	// Zero maxDelay (uncapped)
	if d := outbox.BackoffWithJitter(2, 10*time.Millisecond, 0); d < 0 || d > 40*time.Millisecond {
		t.Fatalf("expected uncapped delay to be within 40ms, got %v", d)
	}
}

func TestRelay_DeadLetter_MaxRetries(t *testing.T) {
	store := newMockStore()
	evt, err := outbox.NewEvent("Account", "ACC-100", "AccountSuspended", map[string]string{"reason": "fraud"}, 2)
	if err != nil {
		t.Fatalf("NewEvent failed: %v", err)
	}
	_ = store.Insert(context.Background(), nil, *evt)

	publishCount := 0
	publisher := outbox.PublisherFunc(func(ctx context.Context, event outbox.Event) error {
		publishCount++
		return errors.New("transient network timeout")
	})

	relay, err := outbox.NewRelay(outbox.RelayConfig{
		Store:        store,
		Publisher:    publisher,
		BackoffBase:  10 * time.Millisecond,
		PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRelay failed: %v", err)
	}

	// Attempt 1: retry_count becomes 1 (< max_retries 2) -> remains PENDING
	count, err := relay.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 event processed, got %d", count)
	}
	if store.events[0].Status != outbox.StatusPending {
		t.Fatalf("expected status PENDING after 1 failure, got %s", store.events[0].Status)
	}
	if store.events[0].RetryCount != 1 {
		t.Fatalf("expected retry_count 1, got %d", store.events[0].RetryCount)
	}
	if store.failedEvents[evt.ID].finalFail {
		t.Fatalf("expected finalFail to be false on attempt 1")
	}

	// Attempt 2: retry_count becomes 2 (>= max_retries 2) -> transitions to StatusDeadLetter
	store.events[0].NextRetryAt = time.Now().UTC().Add(-time.Second)
	count, err = relay.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 event processed, got %d", count)
	}
	if store.events[0].Status != outbox.StatusDeadLetter {
		t.Fatalf("expected status %s after exhausting max retries, got %s", outbox.StatusDeadLetter, store.events[0].Status)
	}
	if !store.events[0].IsDeadLetter() {
		t.Fatalf("expected IsDeadLetter() to be true")
	}
	if store.events[0].RetryCount != 2 {
		t.Fatalf("expected retry_count 2, got %d", store.events[0].RetryCount)
	}
	if !store.failedEvents[evt.ID].finalFail {
		t.Fatalf("expected finalFail to be true after exceeding max retries")
	}

	// Attempt 3: FetchPendingBatch should now skip the dead_letter event
	batch, err := store.FetchPendingBatch(context.Background(), 10)
	if err != nil {
		t.Fatalf("FetchPendingBatch failed: %v", err)
	}
	if len(batch) != 0 {
		t.Fatalf("expected 0 pending events after dead_lettering, got %d", len(batch))
	}
}

func TestRelay_DeadLetter_PoisonPill(t *testing.T) {
	store := newMockStore()
	// Event configured with 10 max retries — should NOT wait for 10 attempts
	evt, err := outbox.NewEvent("Order", "ORD-999", "OrderPlaced", map[string]string{"bad": "data"}, 10)
	if err != nil {
		t.Fatalf("NewEvent failed: %v", err)
	}
	_ = store.Insert(context.Background(), nil, *evt)

	publishCalls := 0
	publisher := outbox.PublisherFunc(func(ctx context.Context, event outbox.Event) error {
		publishCalls++
		return outbox.ErrPoisonPill
	})

	relay, err := outbox.NewRelay(outbox.RelayConfig{
		Store:     store,
		Publisher: publisher,
	})
	if err != nil {
		t.Fatalf("NewRelay failed: %v", err)
	}

	count, err := relay.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 processed event, got %d", count)
	}
	if publishCalls != 1 {
		t.Fatalf("expected exactly 1 publish attempt, got %d", publishCalls)
	}

	// Verify immediate transition to StatusDeadLetter on attempt 1
	if store.events[0].Status != outbox.StatusDeadLetter {
		t.Fatalf("expected poison pill event to immediately enter %s, got %s", outbox.StatusDeadLetter, store.events[0].Status)
	}
	if !store.events[0].IsDeadLetter() {
		t.Fatalf("expected IsDeadLetter() to be true")
	}
	if !store.failedEvents[evt.ID].finalFail {
		t.Fatalf("expected finalFail to be true for poison pill")
	}
}

func TestRelay_DeadLetter_WrappedNonRetryable(t *testing.T) {
	store := newMockStore()
	evt, _ := outbox.NewEvent("Customer", "CUST-1", "CustomerCreated", map[string]string{"id": "1"}, 5)
	_ = store.Insert(context.Background(), nil, *evt)

	publisher := outbox.PublisherFunc(func(ctx context.Context, event outbox.Event) error {
		return outbox.WrapNonRetryable(errors.New("schema validation failed: missing required customer_id"))
	})

	relay, _ := outbox.NewRelay(outbox.RelayConfig{
		Store:     store,
		Publisher: publisher,
	})

	count, err := relay.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 processed event, got %d", count)
	}

	if store.events[0].Status != outbox.StatusDeadLetter {
		t.Fatalf("expected wrapped non-retryable event to transition to %s, got %s", outbox.StatusDeadLetter, store.events[0].Status)
	}
	if !store.failedEvents[evt.ID].finalFail {
		t.Fatalf("expected finalFail to be true")
	}
}

func TestRelay_DeadLetter_MalformedJSON(t *testing.T) {
	store := newMockStore()
	// Manually construct event with corrupted payload bytes
	evt := &outbox.Event{
		ID:            uuid.New(),
		AggregateType: "Telemetry",
		AggregateID:   "TEL-404",
		EventType:     "MetricRecorded",
		Payload:       []byte("{bad-json-payload-corrupted"),
		Status:        outbox.StatusPending,
		RetryCount:    0,
		MaxRetries:    5,
		ScheduledAt:   time.Now().UTC(),
		CreatedAt:     time.Now().UTC(),
	}
	_ = store.Insert(context.Background(), nil, *evt)

	publisher := outbox.PublisherFunc(func(ctx context.Context, event outbox.Event) error {
		var target map[string]any
		return event.UnmarshalPayload(&target) // will return json.SyntaxError
	})

	relay, _ := outbox.NewRelay(outbox.RelayConfig{
		Store:     store,
		Publisher: publisher,
	})

	count, err := relay.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 processed event, got %d", count)
	}

	if store.events[0].Status != outbox.StatusDeadLetter {
		t.Fatalf("expected malformed JSON event to transition to %s, got %s", outbox.StatusDeadLetter, store.events[0].Status)
	}
	if !store.failedEvents[evt.ID].finalFail {
		t.Fatalf("expected finalFail to be true for malformed payload")
	}
}

func TestPGStore_MarkFailed_StatusDeadLetter(t *testing.T) {
	var capturedArgs []any
	mockOp := &mockDBOperator{
		execFunc: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			capturedArgs = args
			return pgconn.NewCommandTag("UPDATE 1"), nil
		},
	}

	store := outbox.NewPGStore(mockOp)
	evtID := uuid.New()

	// Transient failure (finalFail = false) -> status should be PENDING
	err := store.MarkFailed(context.Background(), evtID, "transient error", time.Now().Add(5*time.Second), false)
	if err != nil {
		t.Fatalf("MarkFailed failed: %v", err)
	}
	if len(capturedArgs) < 1 || capturedArgs[0] != outbox.StatusPending {
		t.Fatalf("expected status $1 to be %s, got %v", outbox.StatusPending, capturedArgs[0])
	}

	// Terminal failure (finalFail = true) -> status should be DEAD_LETTER
	err = store.MarkFailed(context.Background(), evtID, "permanent poison pill", time.Now(), true)
	if err != nil {
		t.Fatalf("MarkFailed terminal failed: %v", err)
	}
	if len(capturedArgs) < 1 || capturedArgs[0] != outbox.StatusDeadLetter {
		t.Fatalf("expected status $1 to be %s, got %v", outbox.StatusDeadLetter, capturedArgs[0])
	}
	if capturedArgs[0] != outbox.Status("DEAD_LETTER") {
		t.Fatalf("expected status literal to be 'DEAD_LETTER', got %v", capturedArgs[0])
	}
}

func TestEvent_StatusHelpers(t *testing.T) {
	evt, _ := outbox.NewEvent("Order", "123", "Created", map[string]string{"k": "v"})
	if evt.IsDeadLetter() {
		t.Fatalf("new event should not be dead letter")
	}
	if evt.Status.IsTerminal() {
		t.Fatalf("new event status should not be terminal")
	}

	evt.MarkDeadLetter("corrupted schema")
	if !evt.IsDeadLetter() {
		t.Fatalf("expected IsDeadLetter() to be true")
	}
	if evt.Status != outbox.StatusDeadLetter {
		t.Fatalf("expected StatusDeadLetter, got %s", evt.Status)
	}
	if !evt.Status.IsTerminal() {
		t.Fatalf("expected IsTerminal() to be true")
	}
	if evt.LastError != "corrupted schema" {
		t.Fatalf("unexpected LastError: %s", evt.LastError)
	}
}

func TestPGStore_DialectsAndOptions(t *testing.T) {
	mockOp := &mockDBOperator{}

	// 1. Default Postgres Store with CTE atomic leasing state machine
	pgStoreDefault := outbox.NewPGStore(mockOp)
	pgStore, ok := pgStoreDefault.(interface{ FetchPendingQuery() string })
	if !ok {
		t.Fatalf("expected pgStore to implement FetchPendingQuery")
	}

	pgQuery := pgStore.FetchPendingQuery()
	if !strings.Contains(pgQuery, "FOR UPDATE SKIP LOCKED") {
		t.Fatalf("expected query to contain FOR UPDATE SKIP LOCKED, got: %s", pgQuery)
	}
	if !strings.Contains(pgQuery, "LIMIT $1") {
		t.Fatalf("expected Postgres query to contain LIMIT $1, got: %s", pgQuery)
	}
	if !strings.Contains(pgQuery, "locked_until < NOW()") {
		t.Fatalf("expected query to filter on locked_until < NOW(), got: %s", pgQuery)
	}
	if !strings.Contains(pgQuery, "locked_until = NOW() + INTERVAL '60s'") {
		t.Fatalf("expected query to set locked_until lease interval, got: %s", pgQuery)
	}
	if !strings.Contains(pgQuery, "status = 'PROCESSING'") {
		t.Fatalf("expected query to update status to PROCESSING, got: %s", pgQuery)
	}

	// 2. Custom Table Name Option with WithDialect(DialectPostgres)
	customStoreInstance := outbox.NewPGStore(mockOp, outbox.WithDialect(outbox.DialectPostgres), outbox.WithTableName("tenant_outbox"))
	customStore, ok := customStoreInstance.(interface{ FetchPendingQuery() string })
	if !ok {
		t.Fatalf("expected customStore to implement FetchPendingQuery")
	}

	customQuery := customStore.FetchPendingQuery()
	if !strings.Contains(customQuery, "FROM tenant_outbox") {
		t.Fatalf("expected custom table name tenant_outbox, got: %s", customQuery)
	}
	if !strings.Contains(customQuery, "UPDATE tenant_outbox") {
		t.Fatalf("expected custom table name in UPDATE clause, got: %s", customQuery)
	}
}

func TestStorage_Aliases(t *testing.T) {
	mockOp := &mockDBOperator{}
	store := outbox.NewPGStorage(mockOp)
	if store == nil {
		t.Fatalf("expected NewPGStorage to return non-nil Storage")
	}
}

func TestEvent_NextRetryAt_Initialization(t *testing.T) {
	evt, err := outbox.NewEvent("Payment", "pay-100", "PaymentSettled", map[string]string{"amt": "500"})
	if err != nil {
		t.Fatalf("NewEvent failed: %v", err)
	}
	if evt.NextRetryAt.IsZero() {
		t.Fatalf("expected NextRetryAt to be initialized")
	}
	if evt.ScheduledAt.IsZero() {
		t.Fatalf("expected ScheduledAt to be initialized")
	}
	if !evt.NextRetryAt.Equal(evt.ScheduledAt) {
		t.Fatalf("expected NextRetryAt to equal ScheduledAt on initial creation")
	}
}

func TestRelay_MultiWorkerConcurrency(t *testing.T) {
	store := newMockStore()
	const totalEvents = 80
	for i := 0; i < totalEvents; i++ {
		evt, _ := outbox.NewEvent("Invoice", fmt.Sprintf("INV-%d", i), "InvoiceCreated", map[string]int{"num": i})
		_ = store.Insert(context.Background(), nil, *evt)
	}

	var pubMu sync.Mutex
	publishedMap := make(map[uuid.UUID]int)

	publisher := outbox.PublisherFunc(func(ctx context.Context, event outbox.Event) error {
		// Simulate broker network latency
		time.Sleep(1 * time.Millisecond)
		pubMu.Lock()
		publishedMap[event.ID]++
		pubMu.Unlock()
		return nil
	})

	relay, err := outbox.NewRelay(outbox.RelayConfig{
		BatchSize:    10,
		PollInterval: 5 * time.Millisecond,
		Concurrency:  4, // 4 concurrent worker goroutines
		Store:        store,
		Publisher:    publisher,
	})
	if err != nil {
		t.Fatalf("NewRelay failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	relayErrCh := make(chan error, 1)
	go func() {
		relayErrCh <- relay.Start(ctx)
	}()

	// Wait until all events are processed or context times out
	deadline := time.Now().Add(1800 * time.Millisecond)
	for {
		store.mu.Lock()
		pubCount := len(store.publishedIDs)
		store.mu.Unlock()

		if pubCount >= totalEvents {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for concurrent workers: published %d/%d", pubCount, totalEvents)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	<-relayErrCh

	pubMu.Lock()
	defer pubMu.Unlock()

	if len(publishedMap) != totalEvents {
		t.Fatalf("expected %d distinct events published, got %d", totalEvents, len(publishedMap))
	}

	for id, count := range publishedMap {
		if count != 1 {
			t.Fatalf("event %s published %d times (expected exactly 1 - duplicate detected!)", id, count)
		}
	}
}

func TestRelay_ProcessBatch_MarkPublishedError(t *testing.T) {
	store := newMockStore()
	evt, _ := outbox.NewEvent("Invoice", "INV-ERR-1", "InvoiceCreated", map[string]string{"inv": "1"})
	_ = store.Insert(context.Background(), nil, *evt)

	publisherCalled := false
	publisher := outbox.PublisherFunc(func(ctx context.Context, event outbox.Event) error {
		publisherCalled = true
		return nil
	})

	expectedErr := errors.New("db connection closed during MarkPublished")
	store.markPubErr = expectedErr

	relay, err := outbox.NewRelay(outbox.RelayConfig{
		Store:     store,
		Publisher: publisher,
	})
	if err != nil {
		t.Fatalf("NewRelay failed: %v", err)
	}

	count, err := relay.ProcessBatch(context.Background())
	if err == nil {
		t.Fatalf("expected error from ProcessBatch when MarkPublished fails, got nil")
	}
	if !errors.Is(err, expectedErr) && !strings.Contains(err.Error(), expectedErr.Error()) {
		t.Fatalf("expected error to contain %v, got %v", expectedErr, err)
	}
	if count != 0 {
		t.Fatalf("expected processedCount to be 0 on MarkPublished failure, got %d", count)
	}
	if !publisherCalled {
		t.Fatalf("expected publisher to have been called")
	}
	if len(store.publishedIDs) != 0 {
		t.Fatalf("event should not be recorded as published")
	}
}

func TestStore_ConcurrentFetchPendingBatch_NoOverlap(t *testing.T) {
	store := newMockStore()
	const totalEvents = 100
	for i := 0; i < totalEvents; i++ {
		evt, _ := outbox.NewEvent("Order", fmt.Sprintf("ORD-%d", i), "OrderCreated", map[string]int{"n": i})
		_ = store.Insert(context.Background(), nil, *evt)
	}

	const numWorkers = 10
	var wg sync.WaitGroup
	fetchedIDs := make([][]uuid.UUID, numWorkers)

	startGate := make(chan struct{})
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerIdx int) {
			defer wg.Done()
			<-startGate // ensure high contention simultaneous start
			batch, err := store.FetchPendingBatch(context.Background(), 10)
			if err != nil {
				t.Errorf("worker %d: FetchPendingBatch failed: %v", workerIdx, err)
				return
			}
			for _, e := range batch {
				fetchedIDs[workerIdx] = append(fetchedIDs[workerIdx], e.ID)
			}
		}(w)
	}

	close(startGate)
	wg.Wait()

	// Verify all returned events are mutually disjoint (no overlapping leases)
	seen := make(map[uuid.UUID]int)
	totalFetched := 0
	for workerIdx, ids := range fetchedIDs {
		for _, id := range ids {
			totalFetched++
			seen[id]++
			if seen[id] > 1 {
				t.Fatalf("event %s was fetched by multiple workers! Worker %d saw duplicate (count=%d)", id, workerIdx, seen[id])
			}
		}
	}

	if totalFetched != totalEvents {
		t.Fatalf("expected all %d events to be fetched across %d workers, got %d", totalEvents, numWorkers, totalFetched)
	}
	if len(seen) != totalEvents {
		t.Fatalf("expected %d unique events, got %d", totalEvents, len(seen))
	}
}

type mockRows struct {
	rows    [][]any
	index   int
	closed  bool
	err     error
	scanErr error
}

func (m *mockRows) Close() {
	m.closed = true
}

func (m *mockRows) Err() error {
	return m.err
}

func (m *mockRows) CommandTag() pgconn.CommandTag {
	return pgconn.NewCommandTag(fmt.Sprintf("SELECT %d", len(m.rows)))
}

func (m *mockRows) FieldDescriptions() []pgconn.FieldDescription {
	return nil
}

func (m *mockRows) Next() bool {
	if m.closed || m.err != nil {
		return false
	}
	m.index++
	return m.index <= len(m.rows)
}

func (m *mockRows) Scan(dest ...any) error {
	if m.scanErr != nil {
		return m.scanErr
	}
	if m.index < 1 || m.index > len(m.rows) {
		return errors.New("no row to scan")
	}
	curr := m.rows[m.index-1]
	for i, val := range curr {
		if i >= len(dest) {
			break
		}
		if err := reflectAssign(dest[i], val); err != nil {
			return err
		}
	}
	return nil
}

func (m *mockRows) Values() ([]any, error) {
	if m.index < 1 || m.index > len(m.rows) {
		return nil, errors.New("invalid row index")
	}
	return m.rows[m.index-1], nil
}

func (m *mockRows) RawValues() [][]byte {
	return nil
}

func (m *mockRows) Conn() *pgx.Conn {
	return nil
}

func reflectAssign(dest any, src any) error {
	if dest == nil {
		return errors.New("nil destination")
	}
	destVal := reflect.ValueOf(dest)
	if destVal.Kind() != reflect.Pointer || destVal.IsNil() {
		return errors.New("destination must be a non-nil pointer")
	}

	target := destVal.Elem()

	if src == nil {
		if target.Kind() == reflect.Pointer || target.Kind() == reflect.Slice || target.Kind() == reflect.Interface {
			target.Set(reflect.Zero(target.Type()))
			return nil
		}
		return errors.New("cannot assign nil to non-pointer")
	}

	srcVal := reflect.ValueOf(src)

	// Pointer target with non-pointer src (e.g. **time.Time and time.Time)
	if target.Kind() == reflect.Pointer && srcVal.Kind() != reflect.Pointer {
		if srcVal.Type().AssignableTo(target.Type().Elem()) {
			newPtr := reflect.New(srcVal.Type())
			newPtr.Elem().Set(srcVal)
			target.Set(newPtr)
			return nil
		}
	}

	// Pointer target with pointer src (e.g. **time.Time and *time.Time)
	if target.Kind() == reflect.Pointer && srcVal.Kind() == reflect.Pointer {
		if srcVal.Type().AssignableTo(target.Type()) {
			target.Set(srcVal)
			return nil
		}
	}

	if srcVal.Type().AssignableTo(target.Type()) {
		target.Set(srcVal)
		return nil
	}

	if srcVal.Type().ConvertibleTo(target.Type()) {
		target.Set(srcVal.Convert(target.Type()))
		return nil
	}

	return fmt.Errorf("cannot assign %T to %T", src, dest)
}

func TestPGStore_FetchPendingBatch_WithRows(t *testing.T) {
	evtID := uuid.New()
	leaseTok := uuid.New()
	now := time.Now().UTC()
	lockedUntil := now.Add(60 * time.Second)
	lastErr := "upstream timeout"

	mockR := &mockRows{
		rows: [][]any{
			{
				evtID,                   // 0: id
				"Invoice",               // 1: aggregate_type
				"INV-999",               // 2: aggregate_id
				"InvoiceCreated",        // 3: event_type
				[]byte(`{"inv":999}`),   // 4: payload
				1,                       // 5: retry_count
				5,                       // 6: max_retries
				outbox.StatusProcessing, // 7: status
				&lockedUntil,            // 8: locked_until
				now,                     // 9: created_at
				nil,                     // 10: published_at
				&lastErr,                // 11: last_error
				&leaseTok,               // 12: lease_token
			},
		},
	}

	mockOp := &mockDBOperator{
		queryFunc: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return mockR, nil
		},
	}

	store := outbox.NewPGStore(mockOp, outbox.WithLeaseDuration(45*time.Second))
	batch, err := store.FetchPendingBatch(context.Background(), 10)
	if err != nil {
		t.Fatalf("FetchPendingBatch failed: %v", err)
	}

	if len(batch) != 1 {
		t.Fatalf("expected 1 event, got %d", len(batch))
	}

	evt := batch[0]
	if evt.ID != evtID {
		t.Errorf("expected ID %v, got %v", evtID, evt.ID)
	}
	if evt.AggregateType != "Invoice" || evt.AggregateID != "INV-999" {
		t.Errorf("unexpected aggregate: %s/%s", evt.AggregateType, evt.AggregateID)
	}
	if evt.Status != outbox.StatusProcessing {
		t.Errorf("expected status PROCESSING, got %s", evt.Status)
	}
	if evt.LeaseToken == nil || *evt.LeaseToken != leaseTok {
		t.Errorf("expected LeaseToken %v, got %v", leaseTok, evt.LeaseToken)
	}
	if evt.LastError != lastErr {
		t.Errorf("expected LastError %q, got %q", lastErr, evt.LastError)
	}
	if !mockR.closed {
		t.Errorf("expected rows to be closed after batch scan")
	}
}

func TestPGStore_FetchPendingBatch_ErrorBranches(t *testing.T) {
	// 1. Query error
	expectedQueryErr := errors.New("db connection refused")
	mockOpErr := &mockDBOperator{
		queryFunc: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return nil, expectedQueryErr
		},
	}
	store := outbox.NewPGStore(mockOpErr)
	_, err := store.FetchPendingBatch(context.Background(), 0) // tests limit <= 0 defaulting to 50
	if !errors.Is(err, expectedQueryErr) {
		t.Fatalf("expected query error, got %v", err)
	}

	// 2. Scan error
	mockRScanErr := &mockRows{
		rows:    [][]any{{uuid.New()}}, // insufficient columns
		scanErr: errors.New("scan failure"),
	}
	mockOpScanErr := &mockDBOperator{
		queryFunc: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return mockRScanErr, nil
		},
	}
	storeScanErr := outbox.NewPGStore(mockOpScanErr)
	_, err = storeScanErr.FetchPendingBatch(context.Background(), 10)
	if err == nil || !strings.Contains(err.Error(), "failed to scan outbox event") {
		t.Fatalf("expected scan failure error, got %v", err)
	}

	// 3. Rows.Err() error
	rowIterErr := errors.New("row iteration aborted")
	mockRIterErr := &mockRows{
		rows: [][]any{},
		err:  rowIterErr,
	}
	mockOpIterErr := &mockDBOperator{
		queryFunc: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return mockRIterErr, nil
		},
	}
	storeIterErr := outbox.NewPGStore(mockOpIterErr)
	_, err = storeIterErr.FetchPendingBatch(context.Background(), 10)
	if !errors.Is(err, rowIterErr) {
		t.Fatalf("expected row iteration error, got %v", err)
	}
}

func TestPGStore_LeaseFencing_ErrLeaseExpired(t *testing.T) {
	mockOpZeroRows := &mockDBOperator{
		execFunc: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			return pgconn.NewCommandTag("UPDATE 0"), nil
		},
	}

	store := outbox.NewPGStore(mockOpZeroRows)
	token := uuid.New()
	evtID := uuid.New()

	// MarkPublished with lease token when 0 rows updated (lease lost)
	err := store.MarkPublished(context.Background(), evtID, token)
	if !errors.Is(err, outbox.ErrLeaseExpired) {
		t.Fatalf("expected ErrLeaseExpired on MarkPublished with stale lease, got %v", err)
	}

	// MarkFailed with lease token when 0 rows updated
	err = store.MarkFailed(context.Background(), evtID, "err", time.Now(), false, token)
	if !errors.Is(err, outbox.ErrLeaseExpired) {
		t.Fatalf("expected ErrLeaseExpired on MarkFailed with stale lease, got %v", err)
	}
}

func TestRelay_ProcessBatch_MarkFailedError(t *testing.T) {
	store := newMockStore()
	evt, _ := outbox.NewEvent("Invoice", "INV-FAIL-1", "InvoiceCreated", map[string]string{"k": "v"})
	_ = store.Insert(context.Background(), nil, *evt)

	publisher := outbox.PublisherFunc(func(ctx context.Context, event outbox.Event) error {
		return errors.New("network failure")
	})

	expectedErr := errors.New("db disconnect during MarkFailed")
	store.markFailErr = expectedErr

	relay, err := outbox.NewRelay(outbox.RelayConfig{
		Store:     store,
		Publisher: publisher,
	})
	if err != nil {
		t.Fatalf("NewRelay failed: %v", err)
	}

	_, err = relay.ProcessBatch(context.Background())
	if err == nil {
		t.Fatalf("expected error from ProcessBatch when MarkFailed fails, got nil")
	}
	if !strings.Contains(err.Error(), expectedErr.Error()) {
		t.Fatalf("expected error to contain %v, got %v", expectedErr, err)
	}
}

func TestPGStore_WithLeaseDuration(t *testing.T) {
	mockOp := &mockDBOperator{}
	storeInstance := outbox.NewPGStore(mockOp, outbox.WithLeaseDuration(120*time.Second))
	pgStore, ok := storeInstance.(interface{ FetchPendingQuery() string })
	if !ok {
		t.Fatalf("expected pgStore to implement FetchPendingQuery")
	}

	query := pgStore.FetchPendingQuery()
	if !strings.Contains(query, "INTERVAL '120s'") {
		t.Fatalf("expected query to contain INTERVAL '120s', got %s", query)
	}
	if !strings.Contains(query, "lease_token = gen_random_uuid()") {
		t.Fatalf("expected query to set lease_token, got %s", query)
	}
}

func TestRelayConfig_LeaseDurationValidation(t *testing.T) {
	store := newMockStore()
	publisher := outbox.PublisherFunc(func(ctx context.Context, event outbox.Event) error { return nil })

	// Case 1: LeaseDuration < 5s
	_, err := outbox.NewRelay(outbox.RelayConfig{
		Store:         store,
		Publisher:     publisher,
		LeaseDuration: 4 * time.Second,
	})
	if !errors.Is(err, outbox.ErrInvalidLeaseDuration) {
		t.Fatalf("expected ErrInvalidLeaseDuration for lease duration < 5s, got %v", err)
	}

	// Case 2: LeaseDuration <= PublishTimeout
	_, err = outbox.NewRelay(outbox.RelayConfig{
		Store:          store,
		Publisher:      publisher,
		PublishTimeout: 10 * time.Second,
		LeaseDuration:  10 * time.Second,
	})
	if !errors.Is(err, outbox.ErrInvalidLeaseDuration) {
		t.Fatalf("expected ErrInvalidLeaseDuration for LeaseDuration <= PublishTimeout, got %v", err)
	}

	// Case 3: LeaseDuration < PublishTimeout
	_, err = outbox.NewRelay(outbox.RelayConfig{
		Store:          store,
		Publisher:      publisher,
		PublishTimeout: 15 * time.Second,
		LeaseDuration:  10 * time.Second,
	})
	if !errors.Is(err, outbox.ErrInvalidLeaseDuration) {
		t.Fatalf("expected ErrInvalidLeaseDuration for LeaseDuration < PublishTimeout, got %v", err)
	}

	// Case 4: Valid custom configuration
	relay, err := outbox.NewRelay(outbox.RelayConfig{
		Store:          store,
		Publisher:      publisher,
		PublishTimeout: 2 * time.Second,
		LeaseDuration:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("unexpected error for valid config: %v", err)
	}
	if relay == nil {
		t.Fatalf("expected non-nil relay")
	}
}

func TestRelay_ProcessBatch_PublishTimeoutBound(t *testing.T) {
	store := newMockStore()
	evt, _ := outbox.NewEvent("Order", "ORD-TIMEOUT-1", "OrderCreated", map[string]string{"k": "v"})
	_ = store.Insert(context.Background(), nil, *evt)

	publishReceivedTimeout := make(chan time.Duration, 1)
	publisher := outbox.PublisherFunc(func(ctx context.Context, event outbox.Event) error {
		deadline, ok := ctx.Deadline()
		if ok {
			publishReceivedTimeout <- time.Until(deadline)
		} else {
			publishReceivedTimeout <- 0
		}
		return nil
	})

	relay, err := outbox.NewRelay(outbox.RelayConfig{
		Store:          store,
		Publisher:      publisher,
		PublishTimeout: 2 * time.Second,
		LeaseDuration:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewRelay failed: %v", err)
	}

	count, err := relay.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 processed, got %d", count)
	}

	select {
	case remaining := <-publishReceivedTimeout:
		if remaining <= 0 || remaining > 2100*time.Millisecond {
			t.Fatalf("expected timeout bound around 2s, got %v", remaining)
		}
	default:
		t.Fatalf("expected publish context to have deadline")
	}
}

func TestRelay_ProcessBatch_ExpiredLeaseSkipped(t *testing.T) {
	past := time.Now().UTC().Add(-5 * time.Second)
	evt, _ := outbox.NewEvent("Order", "ORD-EXPIRED-1", "OrderCreated", map[string]string{"k": "v"})
	evt.LockedUntil = &past

	customStore := &mockStoreExpired{
		events: []outbox.Event{*evt},
	}

	publisherCalled := false
	publisher := outbox.PublisherFunc(func(ctx context.Context, event outbox.Event) error {
		publisherCalled = true
		return nil
	})

	relay, err := outbox.NewRelay(outbox.RelayConfig{
		Store:     customStore,
		Publisher: publisher,
	})
	if err != nil {
		t.Fatalf("NewRelay failed: %v", err)
	}

	count, err := relay.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 processed due to expired lease, got %d", count)
	}
	if publisherCalled {
		t.Fatalf("publisher should not have been called for expired lease")
	}
}

type mockStoreExpired struct {
	events []outbox.Event
}

func (m *mockStoreExpired) Insert(ctx context.Context, op outbox.DBOperator, event outbox.Event) error {
	return nil
}
func (m *mockStoreExpired) FetchPendingBatch(ctx context.Context, limit int) ([]outbox.Event, error) {
	return m.events, nil
}
func (m *mockStoreExpired) MarkPublished(ctx context.Context, id uuid.UUID, leaseToken ...uuid.UUID) error {
	return nil
}
func (m *mockStoreExpired) MarkFailed(ctx context.Context, id uuid.UUID, lastErr string, nextRetry time.Time, finalFail bool, leaseToken ...uuid.UUID) error {
	return nil
}

func TestPGStore_MarkPublished_SQLValidation(t *testing.T) {
	var capturedSQL []string
	var capturedArgs [][]any
	mockOp := &mockDBOperator{
		execFunc: func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
			capturedSQL = append(capturedSQL, sql)
			capturedArgs = append(capturedArgs, args)
			return pgconn.NewCommandTag("UPDATE 1"), nil
		},
	}

	store := outbox.NewPGStore(mockOp)
	evtID := uuid.New()
	leaseTok := uuid.New()

	// 1. With lease token
	err := store.MarkPublished(context.Background(), evtID, leaseTok)
	if err != nil {
		t.Fatalf("MarkPublished with token failed: %v", err)
	}
	if len(capturedSQL) != 1 {
		t.Fatalf("expected 1 SQL call, got %d", len(capturedSQL))
	}
	sql1 := capturedSQL[0]
	if !strings.Contains(sql1, "status = 'PUBLISHED'") {
		t.Errorf("expected status = 'PUBLISHED', got: %s", sql1)
	}
	if !strings.Contains(sql1, "published_at = NOW()") {
		t.Errorf("expected published_at = NOW(), got: %s", sql1)
	}
	if !strings.Contains(sql1, "locked_until = NULL") {
		t.Errorf("expected locked_until = NULL, got: %s", sql1)
	}
	if !strings.Contains(sql1, "lease_token = NULL") {
		t.Errorf("expected lease_token = NULL, got: %s", sql1)
	}
	if !strings.Contains(sql1, "WHERE id = $1 AND lease_token = $2") {
		t.Errorf("expected WHERE clause with lease_token, got: %s", sql1)
	}
	if len(capturedArgs[0]) != 2 || capturedArgs[0][0] != evtID || capturedArgs[0][1] != leaseTok {
		t.Errorf("expected args [%v, %v], got %v", evtID, leaseTok, capturedArgs[0])
	}

	// 2. Without lease token
	err = store.MarkPublished(context.Background(), evtID)
	if err != nil {
		t.Fatalf("MarkPublished without token failed: %v", err)
	}
	if len(capturedSQL) != 2 {
		t.Fatalf("expected 2 SQL calls, got %d", len(capturedSQL))
	}
	sql2 := capturedSQL[1]
	if !strings.Contains(sql2, "WHERE id = $1") {
		t.Errorf("expected WHERE id = $1, got: %s", sql2)
	}
	if len(capturedArgs[1]) != 1 || capturedArgs[1][0] != evtID {
		t.Errorf("expected args [%v], got %v", evtID, capturedArgs[1])
	}
}

func TestPGStore_FetchPendingBatch_ComprehensiveCoverage(t *testing.T) {
	evtID1 := uuid.New()
	evtID2 := uuid.New()
	leaseTok := uuid.New()
	now := time.Now().UTC()
	lockedUntil := now.Add(45 * time.Second)
	publishedAt := now.Add(-10 * time.Second)
	lastErr := "upstream timeout"

	mockR := &mockRows{
		rows: [][]any{
			// Row 1: lastErr == nil, publishedAt != nil, lockedUntil == nil, leaseToken == nil
			{
				evtID1,
				"Order",
				"ORD-1",
				"OrderCreated",
				[]byte(`{}`),
				0,
				5,
				outbox.StatusProcessing,
				nil,
				now,
				&publishedAt,
				nil,
				nil,
			},
			// Row 2: lastErr != nil, publishedAt == nil, lockedUntil != nil, leaseToken != nil
			{
				evtID2,
				"Payment",
				"PAY-2",
				"PaymentProcessed",
				[]byte(`{}`),
				1,
				3,
				outbox.StatusProcessing,
				&lockedUntil,
				now,
				nil,
				&lastErr,
				&leaseTok,
			},
		},
	}

	mockOp := &mockDBOperator{
		queryFunc: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return mockR, nil
		},
	}

	store := outbox.NewPGStore(mockOp)
	batch, err := store.FetchPendingBatch(context.Background(), 10)
	if err != nil {
		t.Fatalf("FetchPendingBatch failed: %v", err)
	}
	if len(batch) != 2 {
		t.Fatalf("expected 2 events, got %d", len(batch))
	}

	// Verify Row 1 mappings
	if batch[0].ID != evtID1 || batch[0].LastError != "" || batch[0].PublishedAt == nil || batch[0].LeaseToken != nil {
		t.Errorf("unexpected mapping for row 1: %+v", batch[0])
	}

	// Verify Row 2 mappings
	if batch[1].ID != evtID2 || batch[1].LastError != lastErr || batch[1].PublishedAt != nil || batch[1].LeaseToken == nil || *batch[1].LeaseToken != leaseTok {
		t.Errorf("unexpected mapping for row 2: %+v", batch[1])
	}

	// Test empty batch scanning
	mockEmptyRows := &mockRows{rows: [][]any{}}
	mockOpEmpty := &mockDBOperator{
		queryFunc: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
			return mockEmptyRows, nil
		},
	}
	storeEmpty := outbox.NewPGStore(mockOpEmpty)
	emptyBatch, err := storeEmpty.FetchPendingBatch(context.Background(), 5)
	if err != nil {
		t.Fatalf("FetchPendingBatch empty failed: %v", err)
	}
	if len(emptyBatch) != 0 {
		t.Fatalf("expected 0 events, got %d", len(emptyBatch))
	}
}

type mockDrainStore struct {
	callCount int
	mu        sync.Mutex
}

func (m *mockDrainStore) Insert(ctx context.Context, op outbox.DBOperator, event outbox.Event) error {
	return nil
}

func (m *mockDrainStore) FetchPendingBatch(ctx context.Context, limit int) ([]outbox.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	if m.callCount == 1 {
		// First call: returns full batch to trigger drain loop
		evts := make([]outbox.Event, limit)
		for i := 0; i < limit; i++ {
			evts[i] = outbox.Event{ID: uuid.New(), EventType: "DrainEvent"}
		}
		return evts, nil
	}
	// Second call in drain loop: returns error to verify logging and break
	return nil, errors.New("drain fetch error")
}

func (m *mockDrainStore) MarkPublished(ctx context.Context, id uuid.UUID, leaseToken ...uuid.UUID) error {
	return nil
}

func (m *mockDrainStore) MarkFailed(ctx context.Context, id uuid.UUID, lastErr string, nextRetry time.Time, finalFail bool, leaseToken ...uuid.UUID) error {
	return nil
}

func TestRelay_RunWorker_DrainErrorLogged(t *testing.T) {
	drainStore := &mockDrainStore{}
	publisher := outbox.PublisherFunc(func(ctx context.Context, event outbox.Event) error {
		return nil
	})

	relay, err := outbox.NewRelay(outbox.RelayConfig{
		BatchSize:    2,
		PollInterval: 10 * time.Millisecond,
		Store:        drainStore,
		Publisher:    publisher,
	})
	if err != nil {
		t.Fatalf("NewRelay failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_ = relay.Start(ctx)

	drainStore.mu.Lock()
	calls := drainStore.callCount
	drainStore.mu.Unlock()

	if calls < 2 {
		t.Fatalf("expected at least 2 fetch calls (initial + drain), got %d", calls)
	}
}

func TestPGStore_TableNameValidation(t *testing.T) {
	mockOp := &mockDBOperator{}

	maliciousNames := []string{
		"outbox_events; DROP TABLE users;--",
		"outbox events",
		"outbox-events",
		"123outbox",
		"outbox.table.extra",
		"",
	}

	for _, name := range maliciousNames {
		t.Run("Reject: "+name, func(t *testing.T) {
			store := outbox.NewPGStore(mockOp, outbox.WithTableName(name))

			err := store.Insert(context.Background(), mockOp, outbox.Event{ID: uuid.New()})
			if !errors.Is(err, outbox.ErrInvalidTableName) {
				t.Errorf("expected ErrInvalidTableName on Insert, got: %v", err)
			}

			_, err = store.FetchPendingBatch(context.Background(), 10)
			if !errors.Is(err, outbox.ErrInvalidTableName) {
				t.Errorf("expected ErrInvalidTableName on FetchPendingBatch, got: %v", err)
			}

			err = store.MarkPublished(context.Background(), uuid.New(), uuid.New())
			if !errors.Is(err, outbox.ErrInvalidTableName) {
				t.Errorf("expected ErrInvalidTableName on MarkPublished, got: %v", err)
			}

			err = store.MarkFailed(context.Background(), uuid.New(), "err", time.Now(), false, uuid.New())
			if !errors.Is(err, outbox.ErrInvalidTableName) {
				t.Errorf("expected ErrInvalidTableName on MarkFailed, got: %v", err)
			}
		})
	}

	validNames := []string{
		"outbox_events",
		"tenant_outbox",
		"public.outbox_events",
		"myschema.custom_events_1",
	}
	for _, name := range validNames {
		t.Run("Allow: "+name, func(t *testing.T) {
			store := outbox.NewPGStore(mockOp, outbox.WithTableName(name))
			if qStore, ok := store.(interface{ FetchPendingQuery() string }); ok {
				q := qStore.FetchPendingQuery()
				if !strings.Contains(q, name) {
					t.Errorf("expected query to contain valid table name %s, got: %s", name, q)
				}
			}
		})
	}
}
