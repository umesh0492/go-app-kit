-- 001_outbox_events.sql: Transactional Outbox table for high-throughput dual-write prevention
-- Supports horizontal poller concurrency via Postgres row-level locking (SKIP LOCKED).

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

-- Composite index on (status, next_retry_at, created_at) to avoid sequence scans under high volume
-- and support zero-contention row-level locked polling (FOR UPDATE SKIP LOCKED)
CREATE INDEX IF NOT EXISTS idx_outbox_poll 
    ON outbox_events (status, next_retry_at, created_at);

-- Index for auditing and aggregate history lookup
CREATE INDEX IF NOT EXISTS idx_outbox_aggregate 
    ON outbox_events (aggregate_type, aggregate_id, created_at DESC);
