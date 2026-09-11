# go-app-kit · v0.1.0

[![CI](https://github.com/umesh0492/go-app-kit/actions/workflows/ci.yml/badge.svg)](https://github.com/umesh0492/go-app-kit/actions/workflows/ci.yml)
[![Code Quality: golangci-lint](https://img.shields.io/badge/code%20quality-golangci--lint-brightgreen?logo=go)](https://golangci-lint.run/)
[![Go Version](https://img.shields.io/badge/Go-%3E%3D1.25.0-00ADD8?logo=go)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Vulnerabilities](https://img.shields.io/badge/govulncheck-0%20vulns-brightgreen)](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)

**Production-grade enterprise application and domain accelerator kit for Go.**

While [`go-libs`](https://github.com/umesh0492/go-libs) provides low-level, zero-dependency microservice systems engineering (resilience, concurrency pools, rate limiting, SRE golden signals), **`go-app-kit`** delivers high-velocity business capabilities: **Indian localized fintech helpers (GSTIN, PAN, IFSC, Aadhaar), transactional outbox with PostgreSQL DDL, multi-channel notifications, PDF document generation with GST invoice templates, partitioned compliance audit logging, and streaming data exports**.

---

## Architecture & Ecosystem Role

```
┌────────────────────────────────────────────────────────────────────────────────┐
│                           Enterprise Microservices                             │
│                     (Invoicing, Orders, Fintech, B2B SaaS)                     │
└──────────────────────────────────────┬─────────────────────────────────────────┘
                                       │ imports
                                       ▼
┌────────────────────────────────────────────────────────────────────────────────┐
│                       github.com/umesh0492/go-app-kit                          │
│                                                                                │
│   ├── india/           GSTIN (mod-36), PAN, IFSC, Aadhaar (Verhoeff D5), INR   │
│   ├── pdf/             HTML-to-PDF compilation & embedded GST Invoice template │
│   ├── notifications/   Multi-channel broker (Email, Slack, Webhook HMAC-SHA256)│
│   ├── outbox/          Postgres Transactional Outbox (SKIP LOCKED + backoff)   │
│   ├── audit/           Partitioned compliance audit trails & JSON state diffs  │
│   └── export/          Low-memory streaming CSV exporter with Excel UTF-8 BOM  │
└──────────────────────────────────────┬─────────────────────────────────────────┘
                                       │ builds upon
                                       ▼
┌────────────────────────────────────────────────────────────────────────────────┐
│                        github.com/umesh0492/go-libs                            │
│                                                                                │
│   ├── workerpool/      Bounded panic-safe concurrent workers                   │
│   ├── circuitbreaker/  Microsecond circuit breaking (Three-state machine)      │
│   ├── ratelimit/       Token Bucket, Leaky Bucket, Sliding Window Counter      │
│   ├── retry/           Jittered exponential backoff                            │
│   └── logger/          Structured context-aware SRE logging                    │
└────────────────────────────────────────────────────────────────────────────────┘
```

---

## Installation & Workspace Configuration

### Standalone Import
When consuming `go-app-kit` in your microservice:
```bash
go get github.com/umesh0492/go-app-kit@v0.1.0
```

### Multi-Module Local Development (`go.work`)
When working across both `go-app-kit` and `go-libs` simultaneously in a monorepo or local directory, use Go workspaces (`go.work`) to cleanly resolve dependencies without hardcoding machine-specific relative paths:

```bash
# Initialize a Go workspace at the root directory containing both repositories
go work init ./go-app-kit ./go-libs
```

With `go.work` in place, any changes in `go-libs` are immediately reflected in `go-app-kit` during compilation, testing, and debugging.

### Standard Distribution
`go-app-kit` contains zero local `replace` directives by default and publishes clean, reproducible builds that fetch `github.com/umesh0492/go-libs` from the Go proxy.

### Concurrency Architecture & Dependency on `go-libs/workerpool`
`go-app-kit`'s asynchronous background execution in `notifications` (async multi-channel fan-out) and `audit` (asynchronous audit log ingestion) imports [`go-libs/workerpool`](https://github.com/umesh0492/go-libs/tree/main/workerpool) directly. It leverages bounded concurrency, graceful draining, panic resilience, and Prometheus saturation metrics without maintaining any duplicated forks.

---

## Package Modules

### 1. `india` - Localized Fintech & Enterprise Compliance Helpers
Zero external dependencies. Implements statutory Indian validation algorithms and formatting:
- **GSTIN** (`ValidateGSTIN`, `ParseGSTIN`, `CalculateGSTINCheckDigit`): 15-character Goods and Services Tax Identification Number validation with official mod-36 check digit calculation and 38 state/UT registries.
- **PAN** (`ValidatePAN`, `ParsePAN`): 10-character Permanent Account Number validation with 4th-character entity mapping (Company, Individual, LLP, HUF, Trust, Government Agency).
- **IFSC** (`ValidateIFSC`, `GetBankCode`, `GetBranchCode`): 11-character RBI Financial System Code validation and branch code extraction.
- **Aadhaar** (`ValidateAadhaar`, `MaskAadhaar`, `FormatAadhaar`): 12-digit UIDAI validation using the official **Verhoeff Dihedral $D_5$ algorithm** with privacy masking (`XXXX-XXXX-1234`).
- **Phone** (`ValidatePhone`, `FormatE164`, `FormatNational`): Indian mobile number validation (`+91`, `91`, or `0` prefix) and standard E.164 normalization.
- **Fintech Money Type** (`india.Money`, `NewMoney`, `NewMoneyFromRupees`, `NewMoneyFromFloat`): Exact integer paise-based arithmetic (`Add`, `Sub`, `Mul`, `MulBasisPoints`, `Percentage`, `Split`), eliminating floating-point rounding errors with JSON and SQL serialization.
- **Currency & Words** (`FormatINRPaise`, `AmountToWordsINR`): Indian number system formatting (`12,34,567.89`) and recursive words converter supporting arbitrary Crores.
- **Financial Year** (`GetFinancialYear`, `CurrentFinancialYear`): Indian fiscal calendar calculation (April 1 to March 31) with fiscal quarters (Q1–Q4).
- **AP/AR Aging Buckets** (`AgingBucket`, `DaysOverdue`): Standard statutory accounts payable/receivable overdue aging (Current, 1-30, 31-60, 61-90, 90+).

```go
import "github.com/umesh0492/go-app-kit/india"

// Exact fintech Money arithmetic
taxable := india.NewMoneyFromFloat(15000.50)
cgst := taxable.Percentage(9.0) // 9% GST
total := taxable.Add(cgst).Add(cgst)
fmt.Println(total.Format()) // "17,700.59"

// Validate GSTIN with official mod-36 checksum
err := india.ValidateGSTIN("27AAPFU0939F1ZV")

// Validate Aadhaar using Verhoeff D5 dihedral algorithm
isValid := india.IsValidAadhaar("234567890128")
```

---

### 2. `pdf` - HTML-to-PDF Document Generator
In-memory compilation via `wkhtmltopdf` (requires host `wkhtmltopdf` binary, NOT Chromium or Google Chrome) with responsive page options and embedded production templates:
- **Options**: Margins (mm), PageSize (`A4`, `Letter`), Orientation (`Portrait`, `Landscape`), DPI, and Title metadata.
- **Embedded Templates**:
  - `pdf.GSTInvoiceTemplate`: Indian GST-compliant B2B Tax Invoice with Supplier/Buyer GSTINs, HSN/SAC codes, CGST/SGST/IGST breakdown, Bank NEFT/RTGS details, and Authorized Signatory block.
  - `pdf.ReceiptTemplate`: Clean, modern payment receipt for SaaS and transaction settlements.

```go
import "github.com/umesh0492/go-app-kit/pdf"

// Compile GST Tax Invoice into in-memory PDF buffer
pdfBuf, err := pdf.GenerateFromTemplate(pdf.GSTInvoiceTemplate, invoiceData,
    pdf.WithPageSize("A4"),
    pdf.WithOrientation("Portrait"),
    pdf.WithMargins(10, 10, 10, 10),
)
```

---

### 3. `notifications` - Multi-Channel Notification Dispatcher
Central broker powered by bounded workerpools (adhering to the [`go-libs/workerpool`](https://github.com/umesh0492/go-libs/tree/main/workerpool) concurrency architecture) supporting sync and async delivery:
- **SMTP Email** (`NewEmailSender`): RFC 2822 / MIME multipart messaging (text/plain, text/html, attachments, and authentication).
- **Slack** (`NewSlackSender`): Structured Slack Webhook adapter with priority color bars (Red for Critical, Orange for High, Blue for Normal, Green for Low) and metadata fields.
- **Webhook** (`NewWebhookSender`, `VerifyWebhook`, `WebhookVerifier`): HTTP POST webhook with `X-Signature-SHA256` HMAC tamper-proofing, replay prevention via timestamp binding, and constant-time signature verification.

```go
import "github.com/umesh0492/go-app-kit/notifications"

broker := notifications.NewBroker(notifications.DefaultConfig())
broker.RegisterSender(notifications.NewEmailSender(emailConfig))
broker.RegisterSender(notifications.NewSlackSender(slackConfig))

// Non-blocking async dispatch via workerpool
err := broker.SendAsync(ctx, notifications.Message{
    Title:      "Invoice INV-2026 Ready",
    Body:       "Your invoice is attached.",
    Priority:   notifications.PriorityHigh,
    Recipients: []string{"billing@client.com"},
    Channels:   []notifications.Channel{notifications.ChannelEmail},
    Attachments: []notifications.Attachment{
        {Filename: "INV-2026.pdf", ContentType: "application/pdf", Data: pdfBytes},
    },
})
```

---

### 4. `outbox` - PostgreSQL Transactional Outbox Engine
Guarantees at-least-once message delivery without dual-write race conditions:
- **DDL** (`001_outbox_events.sql` & `002_outbox_concurrency_index.sql`): Production PostgreSQL schema with composite index `idx_outbox_poll ON outbox_events (status, next_retry_at, created_at)` for high-throughput, contention-free polling, `lease_token UUID` fencing, and `idx_outbox_aggregate ON outbox_events (aggregate_type, aggregate_id, created_at DESC)` for entity history lookups.
- **PostgreSQL Exclusivity**: Operates exclusively with PostgreSQL via `github.com/jackc/pgx/v5` parameterized queries (`$1, $2, ...`). *(Note: No MySQL dialect support is implemented or supported at runtime).*
- **Relay Poller** (`NewRelay`): Queries ready events using `SELECT ... FOR UPDATE SKIP LOCKED` and atomic lease renewal (`WithLeaseDuration`) with fencing tokens to allow multiple service replicas to poll concurrently without duplicate dispatches or lease clobbering.
- **Dead-Lettering & Backoff**: Full-jitter exponential backoff and configurable max retries transitioning unresolvable poison pills or exhausted retries to `DEAD_LETTER`.

```go
import "github.com/umesh0492/go-app-kit/outbox"

// Transactionally write domain event inside database transaction
evt, _ := outbox.NewEvent("Invoice", "INV-100", "InvoiceIssued", invoicePayload)
err := outboxStore.Insert(ctx, tx, *evt)

// Autonomous background relay
relay, _ := outbox.NewRelay(outbox.RelayConfig{
    Store:        outboxStore,
    Publisher:    kafkaPublisher,
    PollInterval: 1 * time.Second,
})
go relay.Start(ctx)
```

---

### 5. `audit` - Audit Trail & State Diffing
Structured audit logging with relational persistence and change tracking:
- **DDL** (`001_audit_logs.sql`): Partitioned by range on `created_at` with trigger-enforced append-only constraints (`trg_prevent_audit_log_modification`).
- **State Diffing** (`ComputeDiff`): Computes field-level property changes (`Old` vs `New`) between before and after JSON states.
- **Context Extraction**: Pulls `Actor`, IP address, and comments from context without contaminating domain signatures.

```go
import "github.com/umesh0492/go-app-kit/audit"

// Computes field-level differences and records asynchronously
event := audit.NewEvent(ctx, "INVOICE_UPDATE", "Invoice", "INV-100", beforeState, afterState)
recorder.RecordAsync(event)
```

---

### 6. `export` - Streaming Data Exporter
High-throughput, low-memory CSV streaming:
- Stream directly to `io.Writer` or `http.ResponseWriter` without buffering complete datasets in memory.
- Prepend UTF-8 BOM (`\xEF\xBB\xBF`) for seamless Microsoft Excel rendering.
- Configurable delimiters (`,`, `;`, `\t`), CRLF endings, and row batch flushing.

```go
import "github.com/umesh0492/go-app-kit/export"

columns := []export.Column[InvoiceRow]{
    {Header: "Invoice ID", Extractor: func(i InvoiceRow) string { return i.ID }},
    {Header: "Amount (INR)", Extractor: func(i InvoiceRow) string { return india.FormatINRPaise(i.AmountPaise) }},
    {Header: "GSTIN", Extractor: func(i InvoiceRow) string { return i.BuyerGSTIN }},
}

streamer := export.NewCSVStreamer(responseWriter, columns, export.WithBOM(true))
for rows.Next() {
    streamer.WriteRow(fetchNextRow())
}
streamer.Flush()
```

---

## When, Where, and Why to Use

| Problem | Recommended Module | Why Use It |
| :--- | :--- | :--- |
| **Indian Tax & Banking Compliance** | `go-app-kit/india` | Zero dependencies; verifies GST mod-36 checksum, Aadhaar Verhoeff $D_5$, PAN legal entities, and IFSC codes. |
| **B2B Billing Documents** | `go-app-kit/pdf` | Pre-bundled GST tax invoice & payment receipt templates; in-memory byte rendering with customizable layout. |
| **Cross-Service Dual-Write Safety** | `go-app-kit/outbox` | Atomically commits domain state and events in the same Postgres TX; `SKIP LOCKED` scales poller across $N$ instances. |
| **Multi-Channel User Alerts** | `go-app-kit/notifications` | Unified broker routing to SMTP, Slack, and HMAC-signed webhooks; non-blocking delivery via `workerpool`. |
| **Audit Logging & State Diffing** | `go-app-kit/audit` | Range-partitioned PostgreSQL table; automated JSON state diffing; asynchronous ingestion with append-only triggers. |
| **Large Report & Data Exports** | `go-app-kit/export` | Low-memory streaming CSV writer with Excel UTF-8 BOM support. |

---

## Reference Implementation

A fully working microservice example combining **GST validation, PDF generation, audit recording, transactional outbox, and notifications** is located in [`examples/invoice_service`](examples/invoice_service):

```bash
cd examples/invoice_service
go test -v ./...
```

---

## 📊 Verified Statement Coverage Status

Coverage across packages in `go-app-kit` is measured using Go's statement-level coverage tool (`go test -coverprofile=coverage.out ./...`):

> **Overall Repository Statement Coverage: 91.6%** (Zero data races across `-race`)

| Package | Purpose | Statement Coverage |
|---|---|---|
| `india` | Statutory Indian validations (GSTIN mod-36, PAN, IFSC, Aadhaar Verhoeff D5, INR Money, Aging) | **97.9%** |
| `export` | Low-memory streaming CSV exporter with Excel UTF-8 BOM & formula injection protection | **93.8%** |
| `notifications` | Multi-channel notification broker (SMTP Email, Slack, Webhook HMAC-SHA256 & versioning) | **92.3%** |
| `outbox` | PostgreSQL transactional outbox engine with row-level locked poller & lease fencing | **89.7%** |
| `audit` | Partitioned PostgreSQL audit logging with automated JSON diffing & append-only triggers | **89.5%** |
| `pdf` | In-memory HTML-to-PDF compilation & embedded GST invoice templates | **79.0%** |
| `examples/invoice_service` | Reference microservice with end-to-end integration test & exact paise math | **82.9%** |
| **Total Statement Coverage** | **Cumulative across all packages** | **91.6%** |

---

## Verification & Quality Gates

Run the full verification suite locally:

```bash
# Run all tests with race detector
make test-race

# Run linter
make lint

# Run Go vulnerability scanner
make vulncheck
```

---

## Known Limitations

- **PDF Generation**: Requires a pre-installed `wkhtmltopdf` binary on the host system (NOT Chromium or Google Chrome). In test environments or CI runners without `wkhtmltopdf`, the pluggable `pdf.Generator` interface can be mocked.
- **Outbox Storage**: Requires PostgreSQL 12+ for `FOR UPDATE SKIP LOCKED` concurrency support.
- **Email Sending**: Uses standard Go `net/smtp` without connection pooling.

---

## License

MIT License. See [LICENSE](LICENSE) for details.
