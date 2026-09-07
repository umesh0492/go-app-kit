-- 001_audit_logs.sql: Immutable compliance audit trail table partitioned by range.
-- Captures who did what, when, from where, and exact state diffs.
-- Immutability is guaranteed via both PostgreSQL trigger enforcement and role permission revocation.

CREATE TABLE IF NOT EXISTS audit_logs (
    id UUID DEFAULT gen_random_uuid(),
    actor_id VARCHAR(64) NOT NULL,
    actor_email VARCHAR(128),
    actor_role VARCHAR(64),
    ip_address VARCHAR(45),
    user_agent TEXT,
    action VARCHAR(64) NOT NULL,
    entity_type VARCHAR(64) NOT NULL,
    entity_id VARCHAR(128) NOT NULL,
    before_state JSONB,
    after_state JSONB,
    diff JSONB,
    comment TEXT,
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- Default partition ensures fail-safe ingestion even before date partitions are pre-provisioned
CREATE TABLE IF NOT EXISTS audit_logs_default 
    PARTITION OF audit_logs DEFAULT;

-- Composite indexes on the partitioned table for rapid temporal lookups
CREATE INDEX IF NOT EXISTS idx_audit_entity 
    ON audit_logs (entity_type, entity_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_actor 
    ON audit_logs (actor_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_action 
    ON audit_logs (action, created_at DESC);

-- Enforce append-only table constraints: immutability is guaranteed via both PostgreSQL trigger and role permission revocation
CREATE OR REPLACE FUNCTION prevent_audit_log_modification()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'Audit logs are strictly append-only and immutable. UPDATE and DELETE operations are forbidden.';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_prevent_audit_log_modification ON audit_logs;
CREATE TRIGGER trg_prevent_audit_log_modification
    BEFORE UPDATE OR DELETE ON audit_logs
    FOR EACH ROW
    EXECUTE FUNCTION prevent_audit_log_modification();

DROP TRIGGER IF EXISTS trg_prevent_audit_log_default_modification ON audit_logs_default;
CREATE TRIGGER trg_prevent_audit_log_default_modification
    BEFORE UPDATE OR DELETE ON audit_logs_default
    FOR EACH ROW
    EXECUTE FUNCTION prevent_audit_log_modification();

-- Defense-in-depth permission revocation: ensure table immutability is enforced at the database role level
REVOKE UPDATE, DELETE, TRUNCATE ON audit_logs FROM PUBLIC, app_user;


