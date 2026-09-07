-- 002_outbox_concurrency_index.sql: Concurrency hardening & composite poll index
-- Hardens Outbox table for high-throughput horizontal poller instances.
-- Adds next_retry_at and locked_until columns, and creates lease index.

ALTER TABLE outbox_events 
    ADD COLUMN IF NOT EXISTS next_retry_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

ALTER TABLE outbox_events 
    ADD COLUMN IF NOT EXISTS locked_until TIMESTAMPTZ;

ALTER TABLE outbox_events 
    ADD COLUMN IF NOT EXISTS lease_token UUID;

UPDATE outbox_events 
    SET next_retry_at = scheduled_at 
    WHERE next_retry_at IS NULL;

-- Drop legacy partial index if present
DROP INDEX IF EXISTS idx_outbox_poll;

-- Composite index to prevent sequence scans under high volume and lock contention
CREATE INDEX IF NOT EXISTS idx_outbox_poll 
    ON outbox_events (status, next_retry_at, created_at);

-- Lease index for atomic batch leasing state machine with SKIP LOCKED
CREATE INDEX IF NOT EXISTS idx_outbox_lease 
    ON outbox_events (status, locked_until, next_retry_at, created_at);

