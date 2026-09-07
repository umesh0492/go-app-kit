package notifications

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Webhook header constants.
const (
	HeaderWebhookTimestamp = "X-Webhook-Timestamp"
	HeaderSignatureSHA256  = "X-Signature-SHA256"
	HeaderAltTimestamp     = "X-Signature-Timestamp"
	HeaderSignatureVersion = "X-Signature-Version"
	SignatureVersionV1     = "v1"
)

// DefaultWebhookTolerance is the default maximum tolerance window (5 minutes)
// allowed between current time and webhook timestamp to reject replayed requests.
const DefaultWebhookTolerance = 5 * time.Minute

var (
	// ErrInvalidSignature is returned when HMAC signature verification fails.
	ErrInvalidSignature = errors.New("invalid webhook signature")
	// ErrEmptySignature is returned when the signature header is empty.
	ErrEmptySignature = errors.New("missing or empty webhook signature")
	// ErrMissingTimestamp is returned when timestamp header is missing or empty.
	ErrMissingTimestamp = errors.New("missing webhook timestamp")
	// ErrInvalidTimestamp is returned when timestamp format cannot be parsed.
	ErrInvalidTimestamp = errors.New("invalid webhook timestamp format")
	// ErrTimestampExpired is returned when timestamp exceeds the configured tolerance window.
	ErrTimestampExpired = errors.New("webhook timestamp expired or outside tolerance window")
)

// WebhookConfig configures a webhook endpoint adapter.
type WebhookConfig struct {
	EndpointURL string
	Secret      string
	Headers     map[string]string
	HTTPClient  HTTPClient
}

type webhookSender struct {
	cfg WebhookConfig
}

// NewWebhookSender creates a generic HTTP webhook sender with HMAC signature support.
func NewWebhookSender(cfg WebhookConfig) Sender {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &webhookSender{cfg: cfg}
}

func (s *webhookSender) Channel() Channel {
	return ChannelWebhook
}

func (s *webhookSender) Send(ctx context.Context, msg Message) error {
	if s.cfg.EndpointURL == "" {
		return fmt.Errorf("webhook endpoint URL cannot be empty")
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.EndpointURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create webhook request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "go-app-kit-webhook/1.0")

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set(HeaderWebhookTimestamp, timestamp)
	req.Header.Set(HeaderSignatureVersion, SignatureVersionV1)

	if s.cfg.Secret != "" {
		sig := ComputeWebhookSignature(s.cfg.Secret, timestamp, payload)
		req.Header.Set(HeaderSignatureSHA256, sig)
	}

	for k, v := range s.cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := s.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("webhook HTTP request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("webhook returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// ComputeWebhookSignature creates an HMAC-SHA256 hex string prefixed with version marker "v1="
// binding timestamp + "." + payload to prevent replay and timing attacks.
func ComputeWebhookSignature(secret, timestamp string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(payload)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

// ParseWebhookTimestamp parses a webhook timestamp string supporting Unix epoch seconds,
// Unix epoch milliseconds, or RFC3339 format.
func ParseWebhookTimestamp(timestampStr string) (time.Time, error) {
	clean := strings.TrimSpace(timestampStr)
	if clean == "" {
		return time.Time{}, ErrMissingTimestamp
	}

	if sec, err := strconv.ParseInt(clean, 10, 64); err == nil {
		if sec > 1e11 {
			// Timestamp in milliseconds
			return time.UnixMilli(sec), nil
		}
		return time.Unix(sec, 0), nil
	}

	if t, err := time.Parse(time.RFC3339, clean); err == nil {
		return t, nil
	}

	return time.Time{}, fmt.Errorf("%w: %q", ErrInvalidTimestamp, timestampStr)
}

// WebhookVerifier validates incoming webhook signatures and timestamp freshness.
type WebhookVerifier struct {
	Secret    string
	Tolerance time.Duration
}

// NewWebhookVerifier creates a WebhookVerifier with the given secret and tolerance window.
// If tolerance <= 0, DefaultWebhookTolerance (5 minutes) is used.
func NewWebhookVerifier(secret string, tolerance time.Duration) *WebhookVerifier {
	if tolerance <= 0 {
		tolerance = DefaultWebhookTolerance
	}
	return &WebhookVerifier{
		Secret:    secret,
		Tolerance: tolerance,
	}
}

func parseTaggedSignature(sig, currentTS string) (cleanSig, ts string) {
	cleanSig = sig
	ts = currentTS
	if strings.Contains(sig, "t=") && strings.Contains(sig, "v1=") {
		parts := strings.Split(sig, ",")
		var sigV1 string
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "t=") && ts == "" {
				ts = strings.TrimPrefix(part, "t=")
			} else if strings.HasPrefix(part, "v1=") {
				sigV1 = strings.TrimPrefix(part, "v1=")
			}
		}
		if sigV1 != "" {
			cleanSig = "v1=" + sigV1
		}
	}
	return cleanSig, ts
}

func verifyLegacyWebhookSignature(secret string, payload []byte, cleanSig, v1Hex string) bool {
	expectedIntermediate := "sha256=" + v1Hex
	return subtle.ConstantTimeCompare([]byte(expectedIntermediate), []byte(cleanSig)) == 1
}

// Verify validates that the webhook timestamp is within tolerance and the HMAC-SHA256 signature is valid.
func (v *WebhookVerifier) Verify(timestamp, signature string, payload []byte) error {
	cleanSig := strings.TrimSpace(signature)
	if cleanSig == "" || v.Secret == "" {
		return ErrInvalidSignature
	}

	cleanSig, cleanTS := parseTaggedSignature(cleanSig, strings.TrimSpace(timestamp))
	if cleanTS == "" {
		return ErrMissingTimestamp
	}

	ts, err := ParseWebhookTimestamp(cleanTS)
	if err != nil {
		return err
	}

	tolerance := v.Tolerance
	if tolerance <= 0 {
		tolerance = DefaultWebhookTolerance
	}

	diff := time.Since(ts)
	if diff < 0 {
		diff = -diff
	}

	if diff > tolerance {
		return fmt.Errorf("%w: delta %v exceeds tolerance %v", ErrTimestampExpired, diff, tolerance)
	}

	expectedV1 := ComputeWebhookSignature(v.Secret, cleanTS, payload)
	if subtle.ConstantTimeCompare([]byte(expectedV1), []byte(cleanSig)) == 1 {
		return nil
	}

	v1Hex := strings.TrimPrefix(expectedV1, "v1=")
	if verifyLegacyWebhookSignature(v.Secret, payload, cleanSig, v1Hex) {
		return nil
	}

	return ErrInvalidSignature
}

// VerifyWebhook checks timestamp freshness within DefaultWebhookTolerance (5 minutes)
// and validates the HMAC-SHA256 signature against replay and tampering attacks.
func VerifyWebhook(secret, timestamp, signature string, payload []byte) error {
	return NewWebhookVerifier(secret, DefaultWebhookTolerance).Verify(timestamp, signature, payload)
}
