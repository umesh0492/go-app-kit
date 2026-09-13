# Changelog

> **Note on Repository History**: History reconstructed on 2026-09-11; see CHANGELOG.md for the real feature timeline.

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.0] - 2026-09-11

### Fixed
- outbox: Validate `WithTableName` SQL identifier against injection using strict regex matching (`170e6b5`).
- outbox: Enforce strict lease token fencing in `MarkPublished` and `MarkFailed`, returning `ErrLeaseExpired` on stale or conflicting lease tokens (`c819672`).
- notifications: Enforce mandatory timestamp freshness and clock-skew tolerance in webhook verification (`VerifyWebhook`, `WebhookVerifier`) to eliminate signature-only replay attacks (`35a62bb`).
- build: Eliminate `go.mod` local `replace` directive for standalone distribution and enforce ban in CI (`6ec1f8a`).
- outbox/ci: Add real PostgreSQL SKIP LOCKED integration test suite (`outbox_integration_test.go`) and dedicated GitHub Actions CI service container job (`b5572c1`, `735d8aa`).
- india: Emit integer paise (`amount_paise`), formatted string, and currency in `Money.MarshalJSON` without float wire values, and support backward-compatible decoding in `Money.UnmarshalJSON` (`cea966d`).
- notifications: Honor context cancellation and deadlines in notification broker `SendAsync` during bounded worker pool queue enqueueing (`3609f19`).
- audit: Clarify append-only table constraints in DDL and documentation to reflect that guarantees apply to application roles while database superusers can bypass (`3bfacc5`).
- ci: Scope gosec G404 and G104 exclusions to test files (`_test.go`) instead of global suppression (`a7fa874`).
- ci: Optimize `check_coverage.sh` to run the test suite once and support coverage profile reuse (`5f787ec`).
- examples/invoice_service: Refactor audit and outbox wire event payloads from float64 to integer paise and formatted currency representations (`aad5e62`).

### Changed
- pdf: Consolidated generator-injection seams down to single `WithGenerator(g Generator) Option` parameter.
- outbox: Streamlined `IsNonRetryable` contract to evaluate `ErrNonRetryable` (via `errors.Is`) and `MarkNonRetryable` (`*NonRetryableError` via `errors.As`), removing implicit reflection and ad-hoc JSON syntax/unmarshal error inspections.
- outbox: Made `FetchPendingQuery()` an unexported method (`fetchPendingQuery()`) on unexported `*pgStore`.

### Removed (BREAKING)
- pdf: Removed `NewGenerator`, `NewWkhtmlGenerator`, `GeneratorFunc`, and `WithGeneratorFunc` in favor of `WithGenerator`.
- outbox: Removed `WrapNonRetryable` alias in favor of `MarkNonRetryable`.
- outbox: Removed redundant `NewPGStorage`, `Storage`, and `PGStorage` aliases in favor of `NewPGStore` and `Store`.
- audit: Removed dead getters `TableName()` and `InsertQuery()` from unexported `*pgRecorder`.

## [0.1.0] - 2026-09-09

Initial public release.

### Added
- audit: Structured audit log writer and trigger DDL for PostgreSQL change data tracking.
- export: CSV streaming export with cell formula injection sanitization.
- india: Statutory PAN, GSTIN, Aadhaar, and IFSC validation helpers, Indian financial year calculations, and integer paise currency formatting.
- notifications: Transactional email composition with RFC 2047 MIME headers and HMAC-SHA256 webhook signature verifier with timestamp tolerance.
- outbox: PostgreSQL transactional outbox implementation using SELECT FOR UPDATE SKIP LOCKED and worker relay.
- pdf: In-memory wkhtmltopdf HTML-to-PDF document compilation with embedded GST invoice and payment receipt templates.
- examples/invoice_service: Reference implementation of an invoice management service.

### Known limitations
- PDF generator requires a pre-installed wkhtmltopdf binary on the host system (NOT Chromium or Google Chrome).
- Outbox storage requires PostgreSQL 12+ for SKIP LOCKED concurrency support.
- Email sending uses standard net/smtp without connection pooling.

### Historical Errata
- Commit `06226c5` subject previously referenced "headless Chrome HTML-to-PDF rendering wrapper"; its diff introduced the `wkhtmltopdf` wrapper.

