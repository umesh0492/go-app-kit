# `notifications`

Unified, multi-channel notification broker supporting SMTP Email with multipart MIME attachments, Slack incoming webhooks with priority color coding, and HMAC-SHA256 signed HTTP webhooks, powered by bounded in-process worker pools.

---

## When to Use

- **Transactional Customer Messaging**: Sending transactional emails (order confirmations, password resets, onboarding welcomes) with embedded HTML templates and in-memory PDF attachments.
- **Ops & Security Alerting**: Pushing operational alerts to internal Slack channels with visual priority coloring (Critical, High, Normal, Low) and structured key-value metadata.
- **External Webhook Dispatching**: Emitting webhook events to partner systems with cryptographic HMAC-SHA256 signatures and timestamps to prevent tampering and replay attacks.
- **Non-Blocking Background Delivery**: Offloading slow external network calls (SMTP handshakes, third-party webhook latency) away from HTTP request handlers using bounded worker pools.

---

## Why It Is Written Like That

1. **Decoupled Channel Architecture (`Broker` & `Sender`)**: Senders implement a minimal `Sender` interface (`Channel() Channel`, `Send(ctx, msg) error`). New notification channels (SMS, WhatsApp, Push notifications) can be registered into the broker at runtime without changing dispatch logic.
2. **Bounded Asynchronous Delivery**: Integrates `workerpool.Pool` for `SendAsync`, allowing callers to dispatch notifications asynchronously without spawning unbounded goroutines or risking memory exhaustion during traffic spikes.
3. **Pure Go MIME Email Generation**: Implements standard RFC 2045 / RFC 2046 multipart MIME compilation (supporting HTML bodies, plain-text fallbacks, and binary document attachments) directly using the Go standard library (`net/smtp` and `mime`) with zero third-party email dependencies.
4. **Tamper-Proof Webhook Signatures & Replay Protection**: The webhook sender calculates HMAC-SHA256 signatures (`X-Signature-SHA256: sha256=<hex>`) combined with Unix epoch timestamps (`X-Webhook-Timestamp`). Incoming verification utilizes `crypto/subtle.ConstantTimeCompare` against timing attacks, paired with a configurable timestamp tolerance window (`DefaultWebhookTolerance = 5 * time.Minute`) to reject replayed payloads.
5. **Visual Slack Priority Triage**: Maps priority enums (`CRITICAL`, `HIGH`, `NORMAL`, `LOW`) directly to Slack attachment color borders (`#ef4444`, `#f97316`, `#3b82f6`, `#10b981`) and extracts message metadata into responsive two-column Slack fields.

---

## Alternatives Evaluated

- **Vendor-Specific SDKs (SendGrid, Mailgun, Slack Go SDK)**: Introduce large dependency graphs, diverging API conventions, and tight lock-in to specific commercial providers.
- **Unbounded Goroutines (`go sender.Send(...)`)**: High-throughput bursts can spawn thousands of simultaneous goroutines, exhausting file descriptors or crashing containers with OOM.
- **External Distributed Task Queues (e.g. Asynq, Celery)**: Require provisioning and monitoring external infrastructure (Redis or RabbitMQ) which is overkill for straightforward transactional notifications.

---

## Comparison Table

| Alternative | Pros | Cons | Why We Chose `go-app-kit/notifications` |
|---|---|---|---|
| **Vendor SDKs (SendGrid, Slack)** | Vendor-specific advanced features | Heavy transitive dependencies, vendor lock-in, fragmented interfaces | `go-app-kit/notifications` unifies all channels under a uniform standard library abstraction. |
| **Unbounded Goroutines** | Zero setup, simple one-liner | No backpressure, unbounded memory and socket allocation under load | `go-app-kit/notifications` uses bounded worker pools for predictable concurrency and resource containment. |
| **Distributed Queue (Asynq / Redis)** | Persistent queue survives container crashes | Requires dedicated Redis deployment, operational complexity | `go-app-kit/notifications` provides instant, zero-infrastructure in-process asynchronous dispatch. |

---

## Usage Example

```go
package main

import (
    "context"
    "time"

    "github.com/umesh0492/go-app-kit/notifications"
)

func main() {
    // 1. Initialize broker with bounded background worker pool
    broker := notifications.NewBroker(notifications.Config{
        Workers:   4,
        QueueSize: 100,
    })
    defer broker.Close()

    // 2. Register channel adapters
    broker.RegisterSender(notifications.NewEmailSender(notifications.EmailConfig{
        Host:     "smtp.mailgun.org",
        Port:     587,
        Username: "postmaster@example.com",
        Password: "secret-password",
        From:     "billing@example.com",
        FromName: "Billing System",
    }))

    broker.RegisterSender(notifications.NewSlackSender(notifications.SlackConfig{
        WebhookURL: "https://hooks.slack.com/services/T00/B00/XXXX",
        Username:   "Billing Bot",
    }))

    broker.RegisterSender(notifications.NewWebhookSender(notifications.WebhookConfig{
        EndpointURL: "https://api.partner.com/webhooks/invoices",
        Secret:      "webhook-signing-secret",
    }))

    // 3. Dispatch multi-channel notification
    msg := notifications.Message{
        Title:      "Invoice Generated: INV-2026-001",
        Body:       "Invoice for ₹59,000.00 has been generated and is ready for payment.",
        Priority:   notifications.PriorityHigh,
        Channels:   []notifications.Channel{notifications.ChannelEmail, notifications.ChannelSlack},
        Recipients: []string{"client@customer.com"},
        Metadata: map[string]string{
            "InvoiceID": "INV-2026-001",
            "Amount":    "₹59,000.00",
        },
        Attachments: []notifications.Attachment{
            {
                Filename:    "invoice.pdf",
                ContentType: "application/pdf",
                Data:        []byte("%PDF-1.4 ..."),
            },
        },
    }

    // Synchronous or asynchronous delivery
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    if err := broker.Send(ctx, msg); err != nil {
        panic(err)
    }
}
```
