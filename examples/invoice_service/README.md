# `examples/invoice_service`

End-to-end reference enterprise microservice demonstrating the composition of `india`, `pdf`, `outbox`, `notifications`, and `audit` packages within a production billing pipeline.

---

## When to Use

- **Reference Architecture Blueprint**: Use this sample as a production-grade template for building B2B financial services, procurement engines, or billing systems in Go.
- **Cross-Module Composition**: Learn how to combine offline statutory validation, in-memory PDF rendering, transactional outbox dispatching, multi-channel notification queuing, and immutable compliance auditing.
- **Unit & Integration Testing Patterns**: See how to write deterministic test suites that mock external dependencies (database pools, SMTP servers, PDF generators) without requiring live third-party infrastructure.

---

## Architecture Flow

```mermaid
sequenceDiagram
    autonumber
    actor Client as Billing API Client
    participant Svc as InvoiceService
    participant India as india (Statutory)
    participant PDF as pdf (Document)
    participant Outbox as outbox (Dual-Write)
    participant Audit as audit (Compliance)
    participant Notif as notifications (Broker)

    Client->>Svc: ProcessInvoice(ctx, tx, InvoiceRequest)
    Note over Svc,India: Step 1: Statutory Validation
    Svc->>India: ValidateGSTIN(supplier) & ValidateGSTIN(buyer)
    Svc->>India: ValidateIFSC(bank)
    Svc->>India: AmountToWordsINR(total)
    
    Note over Svc,PDF: Step 2: Document Generation
    Svc->>PDF: GenerateFromTemplate(GSTInvoiceTemplate, data)
    PDF-->>Svc: PDF Buffer (*bytes.Buffer)

    Note over Svc,Outbox: Step 3: Transactional Outbox
    Svc->>Outbox: Insert(ctx, tx, "InvoiceCreated")

    Note over Svc,Audit: Step 4: Compliance Auditing
    Svc->>Audit: RecordAsync(Event: "GENERATE_INVOICE")

    Note over Svc,Notif: Step 5: Multi-Channel Delivery
    Svc->>Notif: SendAsync(Message: Email + PDF Attachment, Slack)

    Svc-->>Client: Return Generated PDF Bytes
```

---

## Why It Is Written Like That

1. **Fail-Fast Validation**: Statutory checks (`ValidateGSTIN`, `ValidateIFSC`) run before any expensive template rendering or database operations. If an enterprise customer enters a typo in a GSTIN check digit, the request fails in sub-microsecond time with zero database round-trips.
2. **Zero Disk I/O Pipeline**: PDF generation produces an in-memory `*bytes.Buffer` that is immediately passed as an attachment to the notification broker and returned in the HTTP response. No temporary files are written or leaked on container storage.
3. **Guaranteed Event Delivery**: The outbox event is stored transactionally. If an external messaging service (e.g. Kafka or AWS SQS) is temporarily down, the event is safely queued in PostgreSQL with exponential retries.
4. **Non-Blocking Background Side Effects**: Notification dispatch and compliance audit logging are executed asynchronously via bounded worker pools, keeping the API response time minimal.
5. **Deterministic Mock Testing**: The service is completely decoupled from concrete infrastructure. As demonstrated in `invoice_service_test.go`, the entire flow—including PDF generation and outbox persistence—can be tested in milliseconds without external Docker dependencies.

---

## Alternatives Evaluated

| Alternative | Pros | Cons | Why We Chose `go-app-kit/examples/invoice_service` |
|---|---|---|---|
| **Direct Dual-Write (DB + Message Broker sync)** | Simple to write initially | Prone to split-brain failures and ghost records if the broker is unavailable during commit | Transactional Outbox pattern guarantees at-least-once delivery with zero phantom records |
| **External Microservice REST Call for PDF** | Decouples binary dependency | High network latency (100–500ms); extra infrastructure hop and network failure point | In-process PDF rendering via standard interface keeps execution in-memory and fast |
| **Synchronous Notification Dispatch** | Immediate feedback | Client waits for external SMTP and Slack HTTP APIs (1–3s latency penalty) | Asynchronous bounded broker queues keep API responses fast (<20ms) |
| **Direct Database Synchronous Audit Logging** | Straightforward | Database locks and slow query log contention slow down customer transactions | Asynchronous batched audit recorder with context actor propagation |

---

## Usage Example

```go
package main

import (
    "context"
    "fmt"
    "os"

    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/umesh0492/go-app-kit/audit"
    "github.com/umesh0492/go-app-kit/india"
    "github.com/umesh0492/go-app-kit/notifications"
    "github.com/umesh0492/go-app-kit/outbox"
)

func main() {
    ctx := context.Background()
    pool, _ := pgxpool.New(ctx, "postgres://user:pass@localhost:5432/billing_db")
    defer pool.Close()

    // 1. Initialize infrastructure modules
    auditRecorder, _ := audit.NewPGRecorder(audit.Config{DB: pool, Workers: 2, QueueSize: 100})
    defer auditRecorder.Close()

    outboxStore := outbox.NewPGStore(pool)

    broker := notifications.NewBroker(notifications.DefaultConfig())
    defer broker.Close()

    // 2. Initialize invoice domain service
    svc := NewInvoiceService(auditRecorder, outboxStore, broker)

    // 3. Process invoice with context
    ctx = audit.ContextWithActor(ctx, audit.Actor{
        ID:    "usr_finance_admin",
        Email: "billing-lead@supplier.com",
        Role:  "BILLING_ADMIN",
    })
    ctx = audit.WithComment(ctx, "Monthly milestone billing for IT consulting services")

    tx, _ := pool.Begin(ctx)
    defer tx.Rollback(ctx)

    pdfBytes, err := svc.ProcessInvoice(ctx, tx, InvoiceRequest{
        InvoiceNumber: "INV-2026-089",
        SupplierGSTIN: "27AAPFU0939F1ZV",
        SupplierName:  "Acme Tech Solutions Pvt Ltd",
        BuyerGSTIN:    "29AABCT1332L1ZV",
        BuyerName:     "Zenith Retail Enterprises",
        BuyerEmail:    "accounts@zenithretail.in",
        Description:   "Cloud Infrastructure Migration Consulting",
        TaxableAmount: india.NewMoneyFromRupees(150000),
        CGSTRate:      9.0,
        SGSTRate:      9.0,
        BankIFSC:      "HDFC0000060",
        BankAccount:   "50200012345678",
        BankName:      "HDFC Bank",
        Branch:        "Fort, Mumbai",
    })
    if err != nil {
        panic(err)
    }
    _ = tx.Commit(ctx)

    _ = os.WriteFile("invoice.pdf", pdfBytes, 0644)
    fmt.Println("Invoice successfully generated and dispatched!")
}
```
