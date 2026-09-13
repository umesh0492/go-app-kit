# `outbox`

Guaranteed at-least-once domain event delivery eliminating the dual-write problem via transactional PostgreSQL persistence, `FOR UPDATE SKIP LOCKED` non-blocking polling, exponential backoff retries, and dead-letter handling.

---

## Reference Store vs. Custom Schemas

> [!IMPORTANT]
> `NewPGStore(db DBOperator, opts ...StoreOption) Store` is the production-ready reference implementation for PostgreSQL DDL (`ddl/001_outbox_events.sql` and `ddl/002_outbox_concurrency_index.sql`), implementing SKIP LOCKED worker leasing, lease-token fencing, and retry backoff.
>
> This package defines the Store interface; production use requires implementing Store against your schema; see outbox_integration_test.go as the reference for correct SKIP LOCKED + fencing semantics.

---

## When to Use

- **Dual-Write Prevention**: Persisting domain entities (Orders, Invoices, Accounts) and their corresponding domain events within the same atomic ACID database transaction.
- **Asynchronous Event Publishing**: Reliably dispatching events to external message brokers (Apache Kafka, RabbitMQ, AWS SQS, Google Cloud Pub/Sub) or external webhooks without data loss during network blips.
- **Horizontally Scaled Microservices**: Running multiple container instances or multi-goroutine workers polling a shared database without deadlocks, row lock contention, or duplicate message publishing.
- **Resilient Retry Handling**: Automatically rescheduling failed publish attempts with full-jitter exponential backoff and isolating permanently failed events into a dead-letter state (`StatusDeadLetter = "dead_letter"`) with error diagnostics.

---

## Why It Is Written Like That

1. **Transactional Atomicity (`DBOperator`)**: The `Store.Insert` method accepts an abstract `DBOperator`, allowing developers to pass an active `pgx.Tx` transaction. If the business entity write rolls back, the outbox event rolls back with it—guaranteeing zero phantom events.
2. **Non-Blocking Horizontal Concurrency (`FOR UPDATE SKIP LOCKED`)**: Polling queries execute `SELECT ... FOR UPDATE SKIP LOCKED ORDER BY status ASC, next_retry_at ASC, created_at ASC LIMIT $1`. Multiple workers across horizontally scaled microservice pods poll concurrently without lock contention, thread starvation, or duplicate event processing.
3. **High-Efficiency Composite Index (`idx_outbox_poll`)**: Provided DDL defines `CREATE INDEX idx_outbox_poll ON outbox_events (status, next_retry_at, created_at)`. As processed and historical events accumulate to millions of records, the composite index eliminates slow table sequential scans and ensures sub-millisecond polling queries.
4. **Jittered Exponential Backoff & Dead-Lettering**: Failed deliveries calculate the next attempt timestamp (`next_retry_at`) using full-jitter exponential backoff to prevent thundering herds on downstream services. When `max_retries` is reached or when a non-retryable poison pill error (e.g. malformed JSON or schema validation error) is encountered, the record is immediately moved to `StatusDeadLetter` (`"dead_letter"`) and `last_error` is preserved for operational alerting.
5. **Decoupled Transport (`Publisher` Interface)**: Senders implement a one-method interface (`Publish(ctx context.Context, event Event) error`), allowing teams to publish to any message broker or inline test function with zero coupling.

---

## Alternatives Evaluated

- **Direct Broker Calls in Handlers**: Writing to the database and then immediately invoking `kafkaProducer.Publish(...)`. If the application pod crashes or the broker network times out between these two lines, the database is committed but the event is permanently lost.
- **Change Data Capture (CDC / Debezium)**: Streaming changes directly from the PostgreSQL Write-Ahead Log (WAL). While highly efficient at hyper-scale, it demands operating Kafka Connect, Debezium, ZooKeeper/KRaft, and schema registries—massive operational overhead for typical microservices.
- **Table-Locking Polling Workers**: Poller queries without `SKIP LOCKED` require advisory locks or lock entire tables, introducing query latency spikes and preventing horizontal scaling across multiple pods.

---

## Comparison Table

| Alternative | Pros | Cons | Why We Chose `go-app-kit/outbox` |
|---|---|---|---|
| **Direct Broker Publish** | Zero additional database tables | Vulnerable to the dual-write problem; events lost during network blips | `go-app-kit/outbox` guarantees atomic consistency within your PostgreSQL transaction. |
| **CDC / Debezium** | Zero application polling query overhead | Heavy operational infrastructure (Kafka Connect, Debezium, WAL administration) | `go-app-kit/outbox` uses your existing PostgreSQL database with zero external infrastructure. |
| **Standard DB Polling** | Standard SQL queries | Lock contention, duplicate delivery risks, and worker blocking | `go-app-kit/outbox` uses `FOR UPDATE SKIP LOCKED` for zero-contention parallel polling. |

---

## Schema Setup

### Initial Setup (`001_outbox_events.sql`)

Run the included initial DDL ([`outbox/ddl/001_outbox_events.sql`](file:///Users/umesh/Downloads/Vendor-Portal-Design/go-app-kit/outbox/ddl/001_outbox_events.sql)):

```sql
CREATE TABLE IF NOT EXISTS outbox_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_type VARCHAR(64) NOT NULL,
    aggregate_id VARCHAR(128) NOT NULL,
    event_type VARCHAR(128) NOT NULL,
    payload JSONB NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'PENDING', -- PENDING, PROCESSING, PUBLISHED, DEAD_LETTER
    retry_count INT NOT NULL DEFAULT 0,
    max_retries INT NOT NULL DEFAULT 5,
    last_error TEXT,
    scheduled_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    next_retry_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    locked_until TIMESTAMPTZ,
    lease_token UUID,
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Composite index for fast polling queries avoiding sequence scans under high volume
-- and supporting non-blocking row-level locked polling (SELECT ... FOR UPDATE SKIP LOCKED)
CREATE INDEX IF NOT EXISTS idx_outbox_poll 
    ON outbox_events (status, next_retry_at, created_at);

-- Index for auditing and aggregate history lookup
CREATE INDEX IF NOT EXISTS idx_outbox_aggregate 
    ON outbox_events (aggregate_type, aggregate_id, created_at DESC);
```

### Migration for Existing Deployments (`002_outbox_concurrency_index.sql`)

If migrating an existing table, apply [`outbox/ddl/002_outbox_concurrency_index.sql`](file:///Users/umesh/Downloads/Vendor-Portal-Design/go-app-kit/outbox/ddl/002_outbox_concurrency_index.sql):

```sql
ALTER TABLE outbox_events 
    ADD COLUMN IF NOT EXISTS next_retry_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

ALTER TABLE outbox_events 
    ADD COLUMN IF NOT EXISTS locked_until TIMESTAMPTZ;

ALTER TABLE outbox_events 
    ADD COLUMN IF NOT EXISTS lease_token UUID;

UPDATE outbox_events 
    SET next_retry_at = scheduled_at 
    WHERE next_retry_at IS NULL;

DROP INDEX IF EXISTS idx_outbox_poll;

CREATE INDEX IF NOT EXISTS idx_outbox_poll 
    ON outbox_events (status, next_retry_at, created_at);

CREATE INDEX IF NOT EXISTS idx_outbox_lease 
    ON outbox_events (status, locked_until, next_retry_at, created_at);
```

---

## High-Concurrency Deployment Recommendations

When deploying the outbox relay in high-volume, multi-instance production environments (e.g. Kubernetes with multiple replica pods), observe the following best practices:

### 1. Zero-Contention Horizontal Scaling via `FOR UPDATE SKIP LOCKED`
- Standard `SELECT ... FOR UPDATE` causes concurrent worker queries to block waiting on row locks held by other instances, leading to transaction timeouts and lock contention cascades.
- `SELECT ... FOR UPDATE SKIP LOCKED` ensures each poller worker instantly skips any rows currently locked by another worker pod and retrieves only free rows.
- This allows linear horizontal scaling: adding more poller pods or goroutines increases overall throughput without inter-instance lock contention.

### 2. Composite Index Performance (`status, next_retry_at, created_at`)
- Without the composite index `idx_outbox_poll ON outbox_events (status, next_retry_at, created_at)`, PostgreSQL executes sequential table scans across all published and historical rows. Under high event volumes (millions of rows), query latency degrades from microseconds to tens of seconds.
- The composite index satisfies:
  1. Equality/In filter: `status IN ('PENDING', 'PROCESSING')`
  2. Range filter: `next_retry_at <= NOW()`
  3. Ordering: `ORDER BY status ASC, next_retry_at ASC, created_at ASC`
- This ensures PostgreSQL performs an index range scan with zero external sorting and minimal buffer reads.

### 3. Tuning Worker Concurrency & Batch Sizing
- **`Concurrency`**: Configured on `RelayConfig.Concurrency`. Specifies the number of concurrent worker goroutines per process. Default is `1`. Set to `2–8` per pod to saturate available downstream network bandwidth.
- **`BatchSize`**: Recommended between `50` and `200`. Extremely small batches (< 10) increase database roundtrip overhead; excessively large batches (> 500) hold row locks too long during external broker publishing.
- **`PollInterval`**: Set to `250ms–1s` for continuous polling. When a batch returns `count == BatchSize`, the relay immediately drains the next batch without waiting for the ticker, maximizing throughput under surge loads.

### 4. Database Connection Pool Sizing (`pgxpool`)
- Each concurrent worker goroutine in `Relay` checks out a connection during `FetchPendingBatch` and `MarkPublished`/`MarkFailed`.
- Size your connection pool accordingly:
  $$\text{MaxConns} \ge (\text{Replica Pods} \times \text{Worker Concurrency}) + \text{HTTP Request Overhead}$$
- Configure connection health checks (`MaxConnIdleTime: 5m`, `MaxConnLifetime: 1h`) to recycle connections cleanly.

### 5. Stale `PROCESSING` Event Recovery (Crash Recovery)
- If a pod crashes mid-batch, events marked in-flight or held by that worker are released automatically when the connection drops, or remain eligible for retry if the connection terminated abruptly.
- Implement an automated reaper or rely on `status IN ('PENDING', 'PROCESSING') AND next_retry_at <= NOW()` to ensure any uncommitted or stranded events are re-evaluated without operator intervention.

### 6. Archival and Table Partitioning for Hyper-Scale
- In environments generating $> 10\text{M}$ events/month, consider partitioning `outbox_events` by range on `created_at`:
  ```sql
  CREATE TABLE outbox_events (...) PARTITION BY RANGE (created_at);
  ```
- Alternatively, run a lightweight scheduled cron to truncate or archive rows with `status = 'PUBLISHED' AND created_at < NOW() - INTERVAL '7 days'`, keeping the active index compact in PostgreSQL buffer cache (RAM).

---

## Usage Example

```go
package main

import (
    "context"
    "log/slog"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/umesh0492/go-app-kit/outbox"
)

func main() {
    ctx := context.Background()
    pool, _ := pgxpool.New(ctx, "postgres://user:pass@localhost:5432/app_db")
    defer pool.Close()

    // Create Postgres outbox storage with dialect support
    outboxStore := outbox.NewPGStore(pool, outbox.WithDialect(outbox.DialectPostgres))

    // 1. Transactionally write domain entity and outbox event
    tx, err := pool.Begin(ctx)
    if err != nil {
        panic(err)
    }
    defer tx.Rollback(ctx)

    // ... mutate domain state (e.g. INSERT INTO invoices ...)

    event, err := outbox.NewEvent("Invoice", "INV-2026-001", "InvoiceCreated", map[string]any{
        "amount":   59000.00,
        "buyer_id": "CUST-992",
    })
    if err != nil {
        panic(err)
    }

    if err := outboxStore.Insert(ctx, tx, *event); err != nil {
        panic(err)
    }

    if err := tx.Commit(ctx); err != nil {
        panic(err)
    }

    // 2. Start high-concurrency background relay poller (4 concurrent workers)
    relay, err := outbox.NewRelay(outbox.RelayConfig{
        BatchSize:    100,
        PollInterval: 500 * time.Millisecond,
        BackoffBase:  1 * time.Second,
        BackoffMax:   30 * time.Second,
        Concurrency:  4, // 4 parallel worker goroutines with FOR UPDATE SKIP LOCKED
        Store:        outboxStore,
        Publisher: outbox.PublisherFunc(func(ctx context.Context, e outbox.Event) error {
            // Publish to Kafka, RabbitMQ, SQS, etc.
            return nil
        }),
        Logger: slog.Default(),
    })
    if err != nil {
        panic(err)
    }

    // Run relay poller loop in background across all workers
    go relay.Start(ctx)
}
```
