# `audit`

Structured audit logging engine with automated JSON state diffing, actor context extraction, non-blocking asynchronous recording, and range-partitioned PostgreSQL DDL with trigger-enforced append-only constraints for application roles (`trg_prevent_audit_log_modification`; database owner/superuser can bypass unless cryptographic hash-chaining is present).

---

## When to Use

- **Audit Trails & Attribution**: Capturing *who* (actor ID, email, role, IP address, user-agent) did *what* (action, entity type, entity ID), *when* (UTC timestamp), and *why* (auditor comment / business justification).
- **Automated Property Diffing**: Recording fine-grained property modifications (before-and-after values) for sensitive entities (bank accounts, vendor profiles, permissions, tax settings).
- **Append-Only Table Constraints**: Enforcing that historical audit records cannot be mutated or deleted by application roles via PostgreSQL triggers (`trg_prevent_audit_log_modification`); database owner/superuser can bypass unless cryptographic hash-chaining is present.
- **Low-Latency Request Handling**: Persisting audit events asynchronously via bounded worker pools so database audit writes never degrade user-facing p99 response times.

---

## Why It Is Written Like That

1. **PostgreSQL Range Partitioning (`PARTITION BY RANGE (created_at)`)**: Audit tables generate high write volumes over time. Range partitioning by timestamp allows historical data archiving and retention management via instantaneous partition detachment or drops (`DROP TABLE audit_logs_2025_q1`) without slow, lock-heavy `DELETE` queries that cause table bloat. A default partition ensures uninterrupted recording before new date ranges are created.
2. **Automated Deep Structural Diffing (`ComputeDiff`)**: Inspects `before_state` and `after_state` maps using deep reflection, producing structured JSON diffs (`{ "tax_rate": { "old": 18.0, "new": 12.0 } }`). Operators can immediately see modified fields without diffing entire snapshots manually.
3. **Seamless Context Integration**: Automatically extracts actor credentials via `ActorFromContext` and operational justification comments via `audit.Comment(ctx)` (set via `audit.WithComment(ctx, comment)`), bridging HTTP middleware context directly into database rows.
4. **Non-Blocking Asynchronous Execution (`RecordAsync`)**: Backed by `workerpool.Pool`. Incoming audit events are dispatched to a bounded background queue in sub-microsecond time, protecting the primary application transaction from audit write latency.
5. **JSONB Query Flexibility**: Persists snapshots and metadata into native PostgreSQL `JSONB` columns, enabling GIN indexing and arbitrary JSON path queries across dynamic entity properties.

---

## Alternatives Evaluated

- **Application Stdout Logs (ELK / CloudWatch)**: Logging JSON strings to stdout. While simple, application logs can be dropped during log forwarder spikes or truncated by retention filters, lacking relational query capabilities.
- **Database PL/pgSQL Triggers / `pg_audit`**: Triggers capture row changes at the database engine level, but they are completely blind to application context—such as authenticated user email, caller IP address, user agent, or human justification comments.
- **Synchronous In-Line Table Inserts**: Executing an audit insert synchronously inside every HTTP request handler doubles database round-trips and adds 5–20ms of latency to customer-facing requests.

---

## Comparison Table

| Alternative | Pros | Cons | Why We Chose `go-app-kit/audit` |
|---|---|---|---|
| **Stdout Logs (ELK / Datadog)** | Zero extra DB writes | Vulnerable to buffer drops, lack relational query capabilities, non-durable | `go-app-kit/audit` persists durable, structured rows into partitioned PostgreSQL. |
| **PostgreSQL Triggers (`pg_audit`)** | Cannot be bypassed by code | Blind to HTTP actor identity, caller IP address, and business justification comments | `go-app-kit/audit` preserves complete actor identity, network provenance, and contextual notes. |
| **Synchronous Database Inserts** | Deterministic commit order | Adds 5–20ms latency to every user request; blocks transactions on audit failures | `go-app-kit/audit` uses bounded in-process worker pools for non-blocking sub-microsecond dispatch. |

---

## Schema Setup

Run the included DDL ([`audit/ddl/001_audit_logs.sql`](file:///Users/umesh/Downloads/Vendor-Portal-Design/go-app-kit/audit/ddl/001_audit_logs.sql)):

```sql
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

CREATE TABLE IF NOT EXISTS audit_logs_default 
    PARTITION OF audit_logs DEFAULT;

CREATE INDEX IF NOT EXISTS idx_audit_entity 
    ON audit_logs (entity_type, entity_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_actor 
    ON audit_logs (actor_id, created_at DESC);
```

---

## Usage Example

```go
package main

import (
    "context"

    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/umesh0492/go-app-kit/audit"
)

func main() {
    ctx := context.Background()
    pool, _ := pgxpool.New(ctx, "postgres://user:pass@localhost:5432/app_db")
    defer pool.Close()

    // 1. Initialize audit recorder with bounded background worker pool
    recorder, err := audit.NewPGRecorder(audit.Config{
        DB:        pool,
        Workers:   2,
        QueueSize: 200,
    })
    if err != nil {
        panic(err)
    }
    defer recorder.Close()

    // 2. Attach actor context from HTTP middleware
    ctx = audit.ContextWithActor(ctx, audit.Actor{
        ID:        "usr_99812",
        Email:     "compliance-officer@company.com",
        Role:      "ADMIN",
        IPAddress: "203.0.113.195",
        UserAgent: "Mozilla/5.0 ...",
    })

    // Attach business justification comment
    ctx = audit.WithComment(ctx, "Approved KYC re-verification following statutory name change")

    // 3. Define state change
    before := map[string]any{"status": "PENDING_VERIFICATION", "risk_level": "HIGH"}
    after := map[string]any{"status": "VERIFIED", "risk_level": "LOW"}

    // 4. Initialize event (computes diff automatically)
    event := audit.NewEvent(ctx, "UPDATE", "Vendor", "VND-4401", before, after)

    // 5. Record asynchronously without blocking HTTP response
    if err := recorder.RecordAsync(event); err != nil {
        panic(err)
    }
}
```
