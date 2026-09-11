//go:build integration

package outbox_test

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/umesh0492/go-app-kit/outbox"
)

//go:embed ddl/001_outbox_events.sql
var ddl001 string

//go:embed ddl/002_outbox_concurrency_index.sql
var ddl002 string

var (
	sharedTestPool *pgxpool.Pool
	poolInitOnce   sync.Once
	poolInitErr    error
)

func initDockerHost() {
	if os.Getenv("DOCKER_HOST") == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			macDockerSock := filepath.Join(home, ".docker/run/docker.sock")
			if _, err := os.Stat(macDockerSock); err == nil {
				_ = os.Setenv("DOCKER_HOST", "unix://"+macDockerSock)
			}
		}
	}
	if os.Getenv("TESTCONTAINERS_RYUK_DISABLED") == "" {
		_ = os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")
	}
}

func getTestPGXPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	poolInitOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		var dsn string
		if envDSN := os.Getenv("POSTGRES_TEST_URL"); envDSN != "" {
			dsn = envDSN
		} else {
			initDockerHost()

			pgContainer, err := tcpostgres.Run(ctx,
				"postgres:14-alpine",
				tcpostgres.WithDatabase("outbox_test"),
				tcpostgres.WithUsername("postgres"),
				tcpostgres.WithPassword("postgres"),
				testcontainers.WithWaitStrategy(
					wait.ForLog("database system is ready to accept connections").
						WithOccurrence(2).
						WithStartupTimeout(60*time.Second),
				),
			)
			if err != nil {
				poolInitErr = fmt.Errorf("failed to start postgres container: %w", err)
				return
			}

			connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
			if err != nil {
				poolInitErr = fmt.Errorf("failed to get container connection string: %w", err)
				return
			}
			dsn = connStr
		}

		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			poolInitErr = fmt.Errorf("failed to create pgxpool: %w", err)
			return
		}

		if err := pool.Ping(ctx); err != nil {
			pool.Close()
			poolInitErr = fmt.Errorf("failed to ping postgres at %s: %w", dsn, err)
			return
		}

		// Apply DDL schemas: 001_outbox_events.sql and 002_outbox_concurrency_index.sql
		if _, err := pool.Exec(ctx, ddl001); err != nil {
			pool.Close()
			poolInitErr = fmt.Errorf("failed to apply 001_outbox_events.sql: %w", err)
			return
		}
		if _, err := pool.Exec(ctx, ddl002); err != nil {
			pool.Close()
			poolInitErr = fmt.Errorf("failed to apply 002_outbox_concurrency_index.sql: %w", err)
			return
		}

		sharedTestPool = pool
	})

	if poolInitErr != nil {
		t.Fatalf("failed to initialize test postgres instance: %v", poolInitErr)
	}

	return sharedTestPool
}

func resetOutboxTable(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(ctx, "TRUNCATE TABLE outbox_events;")
	require.NoError(t, err, "failed to truncate outbox_events table")
}

// a) FetchPendingQuery: leases rows with a lease_token, sets locked_until;
// a concurrent fetch on another connection/goroutine does NOT return the same rows (proves SKIP LOCKED concurrency).
func TestOutbox_Postgres_SkipLocked_ConcurrentLeasing(t *testing.T) {
	pool := getTestPGXPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resetOutboxTable(t, ctx, pool)

	store := outbox.NewPGStore(pool,
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
			CreatedAt:     time.Now().UTC().Add(time.Duration(i) * time.Millisecond),
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
			defer mu.Unlock()
			for _, e := range events {
				leasedIDs[e.ID]++
				assert.NotNil(t, e.LeaseToken, "leased event must have lease token")
				if e.LeaseToken != nil {
					assert.NotEqual(t, uuid.Nil, *e.LeaseToken, "lease token must not be nil UUID")
				}
				assert.NotNil(t, e.LockedUntil, "leased event must have locked_until set")
				if e.LockedUntil != nil {
					assert.True(t, e.LockedUntil.After(time.Now().UTC()), "locked_until must be in the future")
				}
				assert.Equal(t, outbox.StatusProcessing, e.Status)
			}
		}(w)
	}

	wg.Wait()

	assert.Equal(t, totalEvents, len(leasedIDs), "all 100 events should be leased across workers")
	for id, count := range leasedIDs {
		assert.Equal(t, 1, count, "event %s was leased %d times (expected exactly 1 - zero overlap)", id, count)
	}

	// Subsequent fetch while all events are locked must return 0 rows
	extraEvents, err := store.FetchPendingBatch(ctx, batchSize)
	assert.NoError(t, err)
	assert.Empty(t, extraEvents, "no rows should be available while locked under active leases")
}

// b) Expired lease is re-fetchable after locked_until passes; fresh lease is NOT re-fetchable.
func TestOutbox_Postgres_ExpiredVsFreshLease_Refetchability(t *testing.T) {
	pool := getTestPGXPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	resetOutboxTable(t, ctx, pool)

	store := outbox.NewPGStore(pool,
		outbox.WithLeaseDuration(10*time.Second),
	)

	eventID := uuid.New()
	err := store.Insert(ctx, pool, outbox.Event{
		ID:            eventID,
		AggregateType: "invoice",
		AggregateID:   "inv-101",
		EventType:     "invoice.generated",
		Payload:       []byte(`{"total": 500}`),
		Status:        outbox.StatusPending,
		ScheduledAt:   time.Now().UTC().Add(-5 * time.Second),
		NextRetryAt:   time.Now().UTC().Add(-5 * time.Second),
		CreatedAt:     time.Now().UTC().Add(-5 * time.Second),
	})
	require.NoError(t, err)

	// 1. Initial lease
	events, err := store.FetchPendingBatch(ctx, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, eventID, events[0].ID)
	firstLeaseToken := *events[0].LeaseToken
	firstLockedUntil := *events[0].LockedUntil
	assert.True(t, firstLockedUntil.After(time.Now().UTC()))

	// 2. Fresh lease is NOT re-fetchable
	freshFetch, err := store.FetchPendingBatch(ctx, 1)
	require.NoError(t, err)
	assert.Empty(t, freshFetch, "fresh active lease must NOT be re-fetchable")

	// 3. Simulate lease expiration by updating locked_until to the past
	_, err = pool.Exec(ctx, "UPDATE outbox_events SET locked_until = NOW() - INTERVAL '5 seconds', next_retry_at = NOW() - INTERVAL '5 seconds' WHERE id = $1", eventID)
	require.NoError(t, err)

	// 4. Expired lease is re-fetchable
	reFetch, err := store.FetchPendingBatch(ctx, 1)
	require.NoError(t, err)
	require.Len(t, reFetch, 1, "expired lease MUST be re-fetchable")
	assert.Equal(t, eventID, reFetch[0].ID)
	require.NotNil(t, reFetch[0].LeaseToken)
	assert.NotEqual(t, firstLeaseToken, *reFetch[0].LeaseToken, "re-leased event must have a new distinct lease token")
	require.NotNil(t, reFetch[0].LockedUntil)
	assert.True(t, reFetch[0].LockedUntil.After(time.Now().UTC()), "re-leased event must have fresh locked_until in the future")
}

// c) Stale-leaseholder MarkPublished: calling MarkPublished(ctx, id, oldLeaseToken)
// when the row was re-leased to a new worker returns ErrLeaseExpired (verifies Phase 1 strict fencing).
func TestOutbox_Postgres_StrictLeaseFencing_StaleLeaseholder(t *testing.T) {
	pool := getTestPGXPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	resetOutboxTable(t, ctx, pool)

	store := outbox.NewPGStore(pool,
		outbox.WithLeaseDuration(10*time.Second),
	)

	eventID := uuid.New()
	err := store.Insert(ctx, pool, outbox.Event{
		ID:            eventID,
		AggregateType: "payment",
		AggregateID:   "pay-202",
		EventType:     "payment.authorized",
		Payload:       []byte(`{"amount": 2500}`),
		Status:        outbox.StatusPending,
		ScheduledAt:   time.Now().UTC().Add(-5 * time.Second),
		NextRetryAt:   time.Now().UTC().Add(-5 * time.Second),
		CreatedAt:     time.Now().UTC().Add(-5 * time.Second),
	})
	require.NoError(t, err)

	// Worker 1 acquires initial lease
	events1, err := store.FetchPendingBatch(ctx, 1)
	require.NoError(t, err)
	require.Len(t, events1, 1)
	worker1Lease := *events1[0].LeaseToken

	// Worker 1 hangs; lease expires in database
	_, err = pool.Exec(ctx, "UPDATE outbox_events SET locked_until = NOW() - INTERVAL '2 seconds', next_retry_at = NOW() - INTERVAL '2 seconds' WHERE id = $1", eventID)
	require.NoError(t, err)

	// Worker 2 acquires new lease
	events2, err := store.FetchPendingBatch(ctx, 1)
	require.NoError(t, err)
	require.Len(t, events2, 1)
	worker2Lease := *events2[0].LeaseToken
	assert.NotEqual(t, worker1Lease, worker2Lease, "worker 2 must receive a new lease token")

	// Worker 1 wakes up and attempts MarkPublished with stale token -> ErrLeaseExpired
	err = store.MarkPublished(ctx, eventID, worker1Lease)
	assert.ErrorIs(t, err, outbox.ErrLeaseExpired, "calling MarkPublished with stale lease token must return ErrLeaseExpired")

	// Worker 1 attempts MarkFailed with stale token -> ErrLeaseExpired
	err = store.MarkFailed(ctx, eventID, "worker 1 network error", time.Now().Add(time.Minute), false, worker1Lease)
	assert.ErrorIs(t, err, outbox.ErrLeaseExpired, "calling MarkFailed with stale lease token must return ErrLeaseExpired")

	// Worker 2 completes publish with valid lease token -> Success
	err = store.MarkPublished(ctx, eventID, worker2Lease)
	assert.NoError(t, err, "active leaseholder must successfully mark event published")

	// Verify row state in database
	var status string
	var publishedAt *time.Time
	var currentLease *uuid.UUID
	var currentLock *time.Time
	err = pool.QueryRow(ctx, "SELECT status, published_at, lease_token, locked_until FROM outbox_events WHERE id = $1", eventID).
		Scan(&status, &publishedAt, &currentLease, &currentLock)
	require.NoError(t, err)
	assert.Equal(t, "PUBLISHED", status)
	assert.NotNil(t, publishedAt)
	assert.Nil(t, currentLease)
	assert.Nil(t, currentLock)

	// Calling MarkPublished again after completion must return ErrLeaseExpired
	err = store.MarkPublished(ctx, eventID, worker2Lease)
	assert.ErrorIs(t, err, outbox.ErrLeaseExpired, "re-publishing completed event must return ErrLeaseExpired")
}

// d) Timeout & cancellation: a publish exceeding PublishTimeout is cancelled and the row is NOT double-delivered.
func TestOutbox_Postgres_TimeoutAndCancellation_NoDoubleDelivery(t *testing.T) {
	pool := getTestPGXPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	resetOutboxTable(t, ctx, pool)

	store := outbox.NewPGStore(pool,
		outbox.WithLeaseDuration(30*time.Second),
	)

	eventID := uuid.New()
	err := store.Insert(ctx, pool, outbox.Event{
		ID:            eventID,
		AggregateType: "shipment",
		AggregateID:   "ship-777",
		EventType:     "shipment.dispatched",
		Payload:       []byte(`{"tracking": "TRK-999"}`),
		Status:        outbox.StatusPending,
		MaxRetries:    5,
		ScheduledAt:   time.Now().UTC().Add(-5 * time.Second),
		NextRetryAt:   time.Now().UTC().Add(-5 * time.Second),
		CreatedAt:     time.Now().UTC().Add(-5 * time.Second),
	})
	require.NoError(t, err)

	// Step 1: Hanging publisher that exceeds PublishTimeout
	var slowAttempts int32
	slowPublisher := outbox.PublisherFunc(func(pubCtx context.Context, e outbox.Event) error {
		atomic.AddInt32(&slowAttempts, 1)
		select {
		case <-pubCtx.Done():
			return pubCtx.Err()
		case <-time.After(5 * time.Second):
			return nil
		}
	})

	relay1, err := outbox.NewRelay(outbox.RelayConfig{
		Store:          store,
		Publisher:      slowPublisher,
		PublishTimeout: 100 * time.Millisecond,
		LeaseDuration:  30 * time.Second,
		BatchSize:      1,
	})
	require.NoError(t, err)

	// Process batch: publisher will time out and be cancelled
	processed, err := relay1.ProcessBatch(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 1, processed)
	assert.Equal(t, int32(1), atomic.LoadInt32(&slowAttempts))

	// Verify event was NOT published in database; retry scheduled with error recorded
	var status string
	var publishedAt *time.Time
	var retryCount int
	var lastError *string
	err = pool.QueryRow(ctx, "SELECT status, published_at, retry_count, last_error FROM outbox_events WHERE id = $1", eventID).
		Scan(&status, &publishedAt, &retryCount, &lastError)
	require.NoError(t, err)
	assert.Equal(t, string(outbox.StatusPending), status)
	assert.Nil(t, publishedAt, "timed-out event must NOT have published_at set")
	assert.Equal(t, 1, retryCount)
	require.NotNil(t, lastError)
	assert.Contains(t, *lastError, "context deadline exceeded")

	// Step 2: Make event immediately eligible for retry
	_, err = pool.Exec(ctx, "UPDATE outbox_events SET next_retry_at = NOW() - INTERVAL '1 second' WHERE id = $1", eventID)
	require.NoError(t, err)

	// Step 3: Fast publisher delivers successfully
	var fastDelivered int32
	fastPublisher := outbox.PublisherFunc(func(pubCtx context.Context, e outbox.Event) error {
		atomic.AddInt32(&fastDelivered, 1)
		return nil
	})

	relay2, err := outbox.NewRelay(outbox.RelayConfig{
		Store:          store,
		Publisher:      fastPublisher,
		PublishTimeout: 2 * time.Second,
		LeaseDuration:  30 * time.Second,
		BatchSize:      1,
	})
	require.NoError(t, err)

	processed2, err := relay2.ProcessBatch(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 1, processed2)
	assert.Equal(t, int32(1), atomic.LoadInt32(&fastDelivered))

	// Verify database confirms single successful delivery
	err = pool.QueryRow(ctx, "SELECT status, published_at FROM outbox_events WHERE id = $1", eventID).
		Scan(&status, &publishedAt)
	require.NoError(t, err)
	assert.Equal(t, "PUBLISHED", status)
	assert.NotNil(t, publishedAt)

	// Step 4: Subsequent ProcessBatch must not re-deliver
	processed3, err := relay2.ProcessBatch(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 0, processed3)
	assert.Equal(t, int32(1), atomic.LoadInt32(&fastDelivered), "event must NOT be double-delivered")
}

// TestOutbox_TableNameSanitization_SQLInjection verifies that malicious table names
// are rejected with ErrInvalidTableName and do not execute SQL injection attacks against PostgreSQL.
func TestOutbox_TableNameSanitization_SQLInjection(t *testing.T) {
	pool := getTestPGXPool(t)
	ctx := context.Background()

	maliciousNames := []string{
		"events; DROP TABLE users;--",
		"outbox-events",
		"outbox events",
		"events' OR '1'='1",
		"; DROP TABLE outbox_events;--",
		"outbox; SELECT pg_sleep(5);",
		"events/*comment*/",
		"public.events; SELECT 1;",
		"../../etc/passwd",
		"events`DROP TABLE users`",
	}

	for _, badName := range maliciousNames {
		t.Run("malicious_"+badName, func(t *testing.T) {
			s := outbox.NewPGStore(pool, outbox.WithTableName(badName))

			// 1. Insert must be rejected
			err := s.Insert(ctx, pool, outbox.Event{
				ID:            uuid.New(),
				AggregateType: "test",
				AggregateID:   "t-1",
				EventType:     "test.event",
				Payload:       []byte(`{}`),
				Status:        outbox.StatusPending,
				CreatedAt:     time.Now().UTC(),
			})
			assert.ErrorIs(t, err, outbox.ErrInvalidTableName, "Insert with malicious table name must return ErrInvalidTableName")

			// 2. FetchPendingBatch must be rejected
			events, err := s.FetchPendingBatch(ctx, 10)
			assert.ErrorIs(t, err, outbox.ErrInvalidTableName, "FetchPendingBatch with malicious table name must return ErrInvalidTableName")
			assert.Nil(t, events)

			// 3. MarkPublished must be rejected
			err = s.MarkPublished(ctx, uuid.New())
			assert.ErrorIs(t, err, outbox.ErrInvalidTableName, "MarkPublished with malicious table name must return ErrInvalidTableName")

			// 4. MarkFailed must be rejected
			err = s.MarkFailed(ctx, uuid.New(), "err", time.Now(), false)
			assert.ErrorIs(t, err, outbox.ErrInvalidTableName, "MarkFailed with malicious table name must return ErrInvalidTableName")
		})
	}

	// Verify outbox_events table was not affected by any injection attempt
	var count int
	err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM outbox_events").Scan(&count)
	assert.NoError(t, err, "outbox_events table must remain intact and accessible")

	// Valid table names must pass validation
	validNames := []string{
		"outbox_events",
		"public.outbox_events",
		"events_2026",
		"custom_schema.events_partition_1",
	}
	for _, goodName := range validNames {
		s := outbox.NewPGStore(pool, outbox.WithTableName(goodName))
		assert.NotNil(t, s)
	}
}
