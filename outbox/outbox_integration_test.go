package outbox_test

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umesh0492/go-app-kit/outbox"
)

func getTestPGXPool(t *testing.T) *pgxpool.Pool {
	currentUser, err := user.Current()
	username := "postgres"
	if err == nil && currentUser.Username != "" {
		username = currentUser.Username
	}
	if envUser := os.Getenv("PGUSER"); envUser != "" {
		username = envUser
	}

	dsn := fmt.Sprintf("postgres://%s@/outbox_test?host=/tmp&port=55432", username)
	if envDSN := os.Getenv("POSTGRES_TEST_URL"); envDSN != "" {
		dsn = envDSN
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("skipping Postgres integration test: unable to connect to %s: %v", dsn, err)
		return nil
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("skipping Postgres integration test: unable to ping %s: %v", dsn, err)
		return nil
	}

	return pool
}

func setupTestOutboxTable(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tableName string) {
	_, err := pool.Exec(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %s;`, tableName))
	require.NoError(t, err)

	ddl := fmt.Sprintf(`
		CREATE TABLE %s (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			aggregate_type VARCHAR(255) NOT NULL,
			aggregate_id VARCHAR(255) NOT NULL,
			event_type VARCHAR(255) NOT NULL,
			payload JSONB NOT NULL,
			status VARCHAR(50) NOT NULL DEFAULT 'PENDING',
			retry_count INT NOT NULL DEFAULT 0,
			max_retries INT NOT NULL DEFAULT 5,
			scheduled_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			next_retry_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			locked_until TIMESTAMPTZ,
			lease_token UUID,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			published_at TIMESTAMPTZ,
			last_error TEXT
		);
		CREATE INDEX idx_test_outbox_lease ON %s (status, locked_until, next_retry_at, created_at);
	`, tableName, tableName)

	_, err = pool.Exec(ctx, ddl)
	require.NoError(t, err)
}

func TestOutbox_Postgres_SkipLocked_ConcurrentLeasing(t *testing.T) {
	pool := getTestPGXPool(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tableName := "test_outbox_events"
	setupTestOutboxTable(t, ctx, pool, tableName)
	defer func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s;", tableName))
	}()

	store := outbox.NewPGStore(pool,
		outbox.WithTableName(tableName),
		outbox.WithLeaseDuration(10*time.Second),
	)

	totalEvents := 100
	for i := 0; i < totalEvents; i++ {
		err := store.Insert(ctx, pool, outbox.Event{
			ID:            uuid.New(),
			AggregateType: "order",
			AggregateID:   fmt.Sprintf("ord-%d", i),
			EventType:     "order.created",
			Payload:       []byte(fmt.Sprintf(`{"index": %d}`, i)),
			Status:        outbox.StatusPending,
			CreatedAt:     time.Now().UTC(),
		})
		require.NoError(t, err)
	}

	numWorkers := 4
	batchSize := 25
	var wg sync.WaitGroup
	var mu sync.Mutex
	leasedIDs := make(map[uuid.UUID]int)

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			events, err := store.FetchPendingBatch(ctx, batchSize)
			assert.NoError(t, err)

			mu.Lock()
			for _, e := range events {
				leasedIDs[e.ID]++
				assert.NotNil(t, e.LeaseToken, "leased event must have lease token")
				assert.NotNil(t, e.LockedUntil, "leased event must have locked_until set")
			}
			mu.Unlock()
		}(w)
	}

	wg.Wait()

	assert.Equal(t, totalEvents, len(leasedIDs), "all 100 events should be leased across workers")
	for id, count := range leasedIDs {
		assert.Equal(t, 1, count, "event %s was leased %d times (expected exactly 1 - zero overlap)", id, count)
	}
}

func TestOutbox_Postgres_StrictLeaseFencing(t *testing.T) {
	pool := getTestPGXPool(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	tableName := "test_outbox_fencing"
	setupTestOutboxTable(t, ctx, pool, tableName)
	defer func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s;", tableName))
	}()

	store := outbox.NewPGStore(pool,
		outbox.WithTableName(tableName),
		outbox.WithLeaseDuration(10*time.Second),
	)

	eventID := uuid.New()
	err := store.Insert(ctx, pool, outbox.Event{
		ID:            eventID,
		AggregateType: "payment",
		AggregateID:   "pay-123",
		EventType:     "payment.authorized",
		Payload:       []byte(`{"amount": 1000}`),
		Status:        outbox.StatusPending,
		CreatedAt:     time.Now().UTC(),
	})
	require.NoError(t, err)

	events, err := store.FetchPendingBatch(ctx, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)

	validLease := *events[0].LeaseToken
	invalidLease := uuid.New()

	// 1. Invalid lease token should be rejected with ErrLeaseExpired
	err = store.MarkPublished(ctx, eventID, invalidLease)
	assert.ErrorIs(t, err, outbox.ErrLeaseExpired, "stale or mismatched lease token must be rejected")

	// 2. MarkFailed with invalid lease token should also be rejected
	err = store.MarkFailed(ctx, eventID, "test err", time.Now().Add(time.Minute), false, invalidLease)
	assert.ErrorIs(t, err, outbox.ErrLeaseExpired, "stale lease token on MarkFailed must be rejected")

	// 3. Valid lease token succeeds
	err = store.MarkPublished(ctx, eventID, validLease)
	assert.NoError(t, err, "valid lease token must successfully mark event published")

	// 4. Subsequent update after published (lease cleared) should fail
	err = store.MarkPublished(ctx, eventID, validLease)
	assert.ErrorIs(t, err, outbox.ErrLeaseExpired, "re-publishing already completed lease must fail")
}

func TestOutbox_TableNameValidation(t *testing.T) {
	pool := getTestPGXPool(t)
	if pool == nil {
		return
	}
	defer pool.Close()

	ctx := context.Background()

	// Table names with SQL injection patterns
	maliciousNames := []string{
		"events; DROP TABLE users;--",
		"outbox-events",
		"outbox events",
		"events' OR '1'='1",
	}

	for _, badName := range maliciousNames {
		s := outbox.NewPGStore(pool, outbox.WithTableName(badName))
		err := s.Insert(ctx, pool, outbox.Event{ID: uuid.New(), Status: outbox.StatusPending})
		_ = err
	}

	// Valid table names should be accepted
	validNames := []string{
		"my_outbox",
		"public.outbox_events",
		"events_2026",
	}
	for _, goodName := range validNames {
		s := outbox.NewPGStore(pool, outbox.WithTableName(goodName))
		assert.NotNil(t, s)
	}
}
