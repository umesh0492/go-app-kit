# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- india: Emit integer paise, formatted string, and currency in `Money.MarshalJSON` without floating-point wire values, and decode structured JSON, integer paise, formatted strings, and legacy floats in `Money.UnmarshalJSON`.
- notifications: Honor context cancellation and deadlines in `SendAsync` during bounded worker pool queuing.
- audit: Clarified append-only table constraints in DDL and documentation to reflect that guarantees apply to application roles while database superusers can bypass.
- ci: Scoped gosec G404 and G104 linter exclusions to tests (`_test.go`) instead of global suppression.
- ci: Optimized `check_coverage.sh` to execute the test suite once and support profile reuse.
- examples/invoice_service: Migrated audit and outbox wire event payloads from float64 to integer paise and formatted currency representations.

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
