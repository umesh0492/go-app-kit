# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
- PDF generator requires a pre-installed wkhtmltopdf binary on the host system.
- Outbox storage requires PostgreSQL 12+ for SKIP LOCKED concurrency support.
- Email sending uses standard net/smtp without connection pooling.
