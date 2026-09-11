// Package audit provides structured audit logging with automated JSON state diffing,
// actor context extraction, and non-blocking asynchronous recording.
//
// Audit logs are append-only for application roles; database owner/superuser can bypass
// unless cryptographic hash-chaining is present.
package audit
