package notifications_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/umesh0492/go-app-kit/notifications"
)

type mockSender struct {
	ch       notifications.Channel
	sendFunc func(ctx context.Context, msg notifications.Message) error
}

func (m *mockSender) Channel() notifications.Channel {
	return m.ch
}

func (m *mockSender) Send(ctx context.Context, msg notifications.Message) error {
	if m.sendFunc != nil {
		return m.sendFunc(ctx, msg)
	}
	return nil
}

type mockHTTPClient struct {
	doFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return m.doFunc(req)
}

func TestBroker_Send(t *testing.T) {
	broker := notifications.NewBroker(notifications.DefaultConfig())
	defer broker.Close()

	var emailSent, slackSent bool
	broker.RegisterSender(&mockSender{
		ch: notifications.ChannelEmail,
		sendFunc: func(ctx context.Context, msg notifications.Message) error {
			emailSent = true
			return nil
		},
	})
	broker.RegisterSender(&mockSender{
		ch: notifications.ChannelSlack,
		sendFunc: func(ctx context.Context, msg notifications.Message) error {
			slackSent = true
			return nil
		},
	})

	t.Run("Send to specific channels", func(t *testing.T) {
		emailSent = false
		slackSent = false
		err := broker.Send(context.Background(), notifications.Message{
			Title:      "Test",
			Body:       "Body",
			Recipients: []string{"user@test.com"},
			Channels:   []notifications.Channel{notifications.ChannelEmail},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !emailSent || slackSent {
			t.Fatalf("expected only email to be sent, got email=%v, slack=%v", emailSent, slackSent)
		}
	})

	t.Run("Send without channels defaults to all registered", func(t *testing.T) {
		emailSent = false
		slackSent = false
		err := broker.Send(context.Background(), notifications.Message{
			Title:      "Test",
			Body:       "Body",
			Recipients: []string{"user@test.com"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !emailSent || !slackSent {
			t.Fatalf("expected both email and slack, got email=%v, slack=%v", emailSent, slackSent)
		}
	})

	t.Run("Empty recipients returns error", func(t *testing.T) {
		err := broker.Send(context.Background(), notifications.Message{
			Title: "Test",
			Body:  "Body",
		})
		if !errors.Is(err, notifications.ErrEmptyRecipients) {
			t.Fatalf("expected ErrEmptyRecipients, got: %v", err)
		}
	})

	t.Run("Unregistered channel returns error", func(t *testing.T) {
		err := broker.Send(context.Background(), notifications.Message{
			Title:      "Test",
			Body:       "Body",
			Recipients: []string{"user@test.com"},
			Channels:   []notifications.Channel{notifications.ChannelWebhook},
		})
		if !errors.Is(err, notifications.ErrNoSenderRegistered) {
			t.Fatalf("expected ErrNoSenderRegistered, got: %v", err)
		}
	})
}

func TestBroker_SendAsync(t *testing.T) {
	broker := notifications.NewBroker(notifications.Config{
		Workers:   2,
		QueueSize: 10,
	})
	defer broker.Close()

	var wg sync.WaitGroup
	wg.Add(1)

	broker.RegisterSender(&mockSender{
		ch: notifications.ChannelEmail,
		sendFunc: func(ctx context.Context, msg notifications.Message) error {
			defer wg.Done()
			return nil
		},
	})

	err := broker.SendAsync(context.Background(), notifications.Message{
		Title:      "Async Test",
		Body:       "Async Body",
		Recipients: []string{"user@test.com"},
		Channels:   []notifications.Channel{notifications.ChannelEmail},
	})
	if err != nil {
		t.Fatalf("SendAsync failed: %v", err)
	}

	wg.Wait()
}

func TestBroker_SendAsync_ContextCancellation(t *testing.T) {
	broker := notifications.NewBroker(notifications.Config{
		Workers:   2,
		QueueSize: 10,
	})
	defer broker.Close()

	t.Run("Already canceled context returns context.Canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := broker.SendAsync(ctx, notifications.Message{
			Recipients: []string{"user@test.com"},
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
	})

	t.Run("Expired deadline context returns context.DeadlineExceeded", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()

		err := broker.SendAsync(ctx, notifications.Message{
			Recipients: []string{"user@test.com"},
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected context.DeadlineExceeded, got: %v", err)
		}
	})
}

func TestBroker_Closed(t *testing.T) {
	broker := notifications.NewBroker(notifications.DefaultConfig())
	broker.Close()
	broker.Close() // idempotent

	err := broker.Send(context.Background(), notifications.Message{
		Recipients: []string{"user@test.com"},
	})
	if !errors.Is(err, notifications.ErrBrokerClosed) {
		t.Fatalf("expected ErrBrokerClosed on Send, got: %v", err)
	}

	err = broker.SendAsync(context.Background(), notifications.Message{
		Recipients: []string{"user@test.com"},
	})
	if !errors.Is(err, notifications.ErrBrokerClosed) {
		t.Fatalf("expected ErrBrokerClosed on SendAsync, got: %v", err)
	}
}

func TestEmailSender(t *testing.T) {
	var sentAddr, sentFrom string
	var sentTo []string
	var sentBody []byte

	sender := notifications.NewEmailSender(notifications.EmailConfig{
		Host:     "smtp.example.com",
		Port:     587,
		Username: "user",
		Password: "password",
		From:     "noreply@example.com",
		FromName: "Support Desk",
		SendMailFunc: func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
			sentAddr = addr
			sentFrom = from
			sentTo = to
			sentBody = msg
			return nil
		},
	})

	if sender.Channel() != notifications.ChannelEmail {
		t.Fatalf("expected ChannelEmail, got %s", sender.Channel())
	}

	t.Run("Plain and HTML with Attachment", func(t *testing.T) {
		msg := notifications.Message{
			Title:      "Invoice Generated",
			Body:       "Please find attached your invoice.",
			HTMLBody:   "<h1>Invoice</h1><p>Please find attached.</p>",
			Recipients: []string{"client@domain.com"},
			Attachments: []notifications.Attachment{
				{
					Filename:    "invoice.pdf",
					ContentType: "application/pdf",
					Data:        []byte("%PDF-1.4 Mock PDF Content"),
				},
			},
		}

		err := sender.Send(context.Background(), msg)
		if err != nil {
			t.Fatalf("Send failed: %v", err)
		}

		if sentAddr != "smtp.example.com:587" {
			t.Errorf("unexpected addr: %s", sentAddr)
		}
		if sentFrom != "noreply@example.com" {
			t.Errorf("unexpected from: %s", sentFrom)
		}
		if len(sentTo) != 1 || sentTo[0] != "client@domain.com" {
			t.Errorf("unexpected recipients: %v", sentTo)
		}

		bodyStr := string(sentBody)
		if !strings.Contains(bodyStr, "Subject: Invoice Generated") {
			t.Errorf("subject missing from email body: %s", bodyStr)
		}
		if !strings.Contains(bodyStr, "Support Desk") {
			t.Errorf("from name missing from email body: %s", bodyStr)
		}
		if !strings.Contains(bodyStr, "Date: ") {
			t.Errorf("Date header missing: %s", bodyStr)
		}
		if !strings.Contains(bodyStr, "Message-ID: <") {
			t.Errorf("Message-ID header missing: %s", bodyStr)
		}
		if !strings.Contains(bodyStr, "Content-Transfer-Encoding: base64") {
			t.Errorf("expected base64 content-transfer-encoding: %s", bodyStr)
		}
		if !strings.Contains(bodyStr, "multipart/mixed") {
			t.Errorf("expected multipart/mixed for attachment: %s", bodyStr)
		}
		if !strings.Contains(bodyStr, "multipart/alternative") {
			t.Errorf("expected multipart/alternative for html: %s", bodyStr)
		}
		if !strings.Contains(bodyStr, "filename=\"invoice.pdf\"") {
			t.Errorf("expected attachment filename in body: %s", bodyStr)
		}
	})

	t.Run("CRLF Sanitization in Headers", func(t *testing.T) {
		msg := notifications.Message{
			Title:      "Security Alert\r\nBcc: evil@attacker.com\nInjected-Header: evil",
			Body:       "Malicious test",
			Recipients: []string{"user@domain.com\r\nCc: spy@attacker.com\n"},
			Attachments: []notifications.Attachment{
				{
					Filename: "doc\r\nEvil-Header: test.pdf",
					Data:     []byte("test"),
				},
			},
		}

		err := sender.Send(context.Background(), msg)
		if err != nil {
			t.Fatalf("Send failed: %v", err)
		}

		bodyStr := string(sentBody)
		if strings.Contains(bodyStr, "\r\nBcc:") || strings.Contains(bodyStr, "\nBcc:") || strings.Contains(bodyStr, "\r\nCc:") || strings.Contains(bodyStr, "\nCc:") || strings.Contains(bodyStr, "\r\nInjected-Header:") || strings.Contains(bodyStr, "\nInjected-Header:") || strings.Contains(bodyStr, "\r\nEvil-Header:") || strings.Contains(bodyStr, "\nEvil-Header:") {
			t.Fatalf("CRLF injection detected in headers: %s", bodyStr)
		}
		if !strings.Contains(bodyStr, "Subject: Security AlertBcc: evil@attacker.comInjected-Header: evil") {
			t.Fatalf("expected sanitized subject in body: %s", bodyStr)
		}
		if !strings.Contains(bodyStr, "filename=\"docEvil-Header: test.pdf\"") {
			t.Fatalf("expected sanitized filename: %s", bodyStr)
		}
	})

	t.Run("Attachment Non-ASCII Filename RFC 2047 Encoding", func(t *testing.T) {
		msg := notifications.Message{
			Title:      "Invoice with non-ASCII attachment",
			Body:       "Please find attached document.",
			Recipients: []string{"client@domain.com"},
			Attachments: []notifications.Attachment{
				{
					Filename:    "faktura_złoty_हिंदी.pdf",
					ContentType: "application/pdf",
					Data:        []byte("non-ascii filename test"),
				},
			},
		}

		err := sender.Send(context.Background(), msg)
		if err != nil {
			t.Fatalf("Send failed: %v", err)
		}

		bodyStr := string(sentBody)
		// Must NOT contain Go's %q unicode escape sequence \uXXXX
		if strings.Contains(bodyStr, `\u`) {
			t.Fatalf("expected no \\u Unicode escapes in headers: %s", bodyStr)
		}
		// Must contain RFC 2047 encoded-word syntax (=?UTF-8?b?...?= or =?UTF-8?B?...?=)
		if !strings.Contains(bodyStr, "=?UTF-8?b?") && !strings.Contains(bodyStr, "=?UTF-8?B?") {
			t.Fatalf("expected RFC 2047 encoded-word syntax for non-ASCII attachment filename: %s", bodyStr)
		}
		if !strings.Contains(bodyStr, "filename==?UTF-8?") {
			t.Fatalf("expected attachment filename to be RFC 2047 encoded: %s", bodyStr)
		}
	})

	t.Run("Context canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := sender.Send(ctx, notifications.Message{Recipients: []string{"a@b.com"}})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
	})
}

func TestSlackSender(t *testing.T) {
	t.Run("Success with Priority Colors", func(t *testing.T) {
		priorities := []notifications.Priority{
			notifications.PriorityCritical,
			notifications.PriorityHigh,
			notifications.PriorityNormal,
			notifications.PriorityLow,
		}

		for _, p := range priorities {
			var recordedBody string
			client := &mockHTTPClient{
				doFunc: func(req *http.Request) (*http.Response, error) {
					b, _ := io.ReadAll(req.Body)
					recordedBody = string(b)
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(bytes.NewReader([]byte("ok"))),
					}, nil
				},
			}

			sender := notifications.NewSlackSender(notifications.SlackConfig{
				WebhookURL: "https://hooks.slack.com/services/test/test/test",
				Username:   "AlertBot",
				IconEmoji:  ":fire:",
				HTTPClient: client,
			})

			err := sender.Send(context.Background(), notifications.Message{
				Title:      "System Alert",
				Body:       "Node CPU > 95%",
				Priority:   p,
				Recipients: []string{"#alerts"},
				Metadata:   map[string]string{"Cluster": "prod-mumbai"},
			})
			if err != nil {
				t.Fatalf("Slack send failed for %s: %v", p, err)
			}
			if !strings.Contains(recordedBody, string(p)) {
				t.Fatalf("expected priority %s in slack payload: %s", p, recordedBody)
			}
		}
	})

	t.Run("Missing Webhook URL", func(t *testing.T) {
		sender := notifications.NewSlackSender(notifications.SlackConfig{})
		err := sender.Send(context.Background(), notifications.Message{Title: "Test"})
		if err == nil {
			t.Fatalf("expected error on empty webhook url")
		}
	})

	t.Run("Server Error Response", func(t *testing.T) {
		client := &mockHTTPClient{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Body:       io.NopCloser(bytes.NewReader([]byte("invalid_payload"))),
				}, nil
			},
		}

		sender := notifications.NewSlackSender(notifications.SlackConfig{
			WebhookURL: "https://hooks.slack.com/services/err",
			HTTPClient: client,
		})

		err := sender.Send(context.Background(), notifications.Message{Title: "Test"})
		if err == nil || !strings.Contains(err.Error(), "invalid_payload") {
			t.Fatalf("expected error with response text, got: %v", err)
		}
	})
}

func TestWebhookSender(t *testing.T) {
	secret := "my-secret-key-12345"

	t.Run("Success with HMAC-SHA256 Signature Verification", func(t *testing.T) {
		var sentSignature string
		var sentTimestamp string
		var sentCustomHeader string
		var sentBody []byte

		client := &mockHTTPClient{
			doFunc: func(req *http.Request) (*http.Response, error) {
				sentSignature = req.Header.Get("X-Signature-SHA256")
				sentTimestamp = req.Header.Get("X-Webhook-Timestamp")
				sentSignature = req.Header.Get("X-Signature-SHA256")
				sentTimestamp = req.Header.Get("X-Webhook-Timestamp")
				sentCustomHeader = req.Header.Get("X-Tenant-ID")
				sentBody, _ = io.ReadAll(req.Body)

				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader([]byte("{\"status\":\"received\"}"))),
				}, nil
			},
		}

		sender := notifications.NewWebhookSender(notifications.WebhookConfig{
			EndpointURL: "https://api.partner.com/webhooks/incoming",
			Secret:      secret,
			Headers:     map[string]string{"X-Tenant-ID": "tenant-99"},
			HTTPClient:  client,
		})

		msg := notifications.Message{
			ID:         "evt-1001",
			Title:      "Invoice Settled",
			Body:       "Payment for INV-001 confirmed",
			Priority:   notifications.PriorityNormal,
			Recipients: []string{"tenant-99"},
		}

		err := sender.Send(context.Background(), msg)
		if err != nil {
			t.Fatalf("webhook send failed: %v", err)
		}

		if sentSignature == "" || !strings.HasPrefix(sentSignature, "v1=") {
			t.Fatalf("missing or invalid signature: %s", sentSignature)
		}
		if sentTimestamp == "" {
			t.Fatalf("missing timestamp")
		}
		if sentCustomHeader != "tenant-99" {
			t.Fatalf("custom header mismatch: %s", sentCustomHeader)
		}

		// Verify using VerifyWebhook
		if err := notifications.VerifyWebhook(secret, sentTimestamp, sentSignature, sentBody); err != nil {
			t.Fatalf("VerifyWebhook failed for valid signature: %v", err)
		}

		verifier := notifications.NewWebhookVerifier(secret, notifications.DefaultWebhookTolerance)
		if err := verifier.Verify(sentTimestamp, sentSignature, sentBody); err != nil {
			t.Fatalf("verifier.Verify failed for valid signature: %v", err)
		}

		// Tamper payload test
		tamperedBody := append(sentBody, byte('!'))
		if err := notifications.VerifyWebhook(secret, sentTimestamp, sentSignature, tamperedBody); err == nil {
			t.Fatalf("VerifyWebhook should fail for tampered payload")
		}

		// Bad secret test
		if err := notifications.VerifyWebhook("wrong-secret", sentTimestamp, sentSignature, sentBody); err == nil {
			t.Fatalf("VerifyWebhook should fail with wrong secret")
		}
	})

	t.Run("Missing Endpoint URL", func(t *testing.T) {
		sender := notifications.NewWebhookSender(notifications.WebhookConfig{})
		err := sender.Send(context.Background(), notifications.Message{})
		if err == nil {
			t.Fatalf("expected error with missing endpoint url")
		}
	})

	t.Run("HTTP Error Status", func(t *testing.T) {
		client := &mockHTTPClient{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(bytes.NewReader([]byte("Internal Server Error"))),
				}, nil
			},
		}

		sender := notifications.NewWebhookSender(notifications.WebhookConfig{
			EndpointURL: "https://api.partner.com/webhook",
			HTTPClient:  client,
		})

		err := sender.Send(context.Background(), notifications.Message{Title: "Test"})
		if err == nil || !strings.Contains(err.Error(), "500") {
			t.Fatalf("expected 500 error, got: %v", err)
		}
	})
}

func TestWebhook_ValidSignatureAndCurrentTimestamp(t *testing.T) {
	secret := "webhook-test-secret-abcdef123456"
	payload := []byte(`{"event":"order.completed","order_id":"ord-998822","amount":1500}`)
	now := time.Now()
	currentTS := strconv.FormatInt(now.Unix(), 10)

	sig := notifications.ComputeWebhookSignature(secret, currentTS, payload)

	t.Run("Current Unix seconds timestamp passes", func(t *testing.T) {
		if err := notifications.VerifyWebhook(secret, currentTS, sig, payload); err != nil {
			t.Fatalf("expected VerifyWebhook to pass, got: %v", err)
		}

		verifier := notifications.NewWebhookVerifier(secret, 5*time.Minute)
		if err := verifier.Verify(currentTS, sig, payload); err != nil {
			t.Fatalf("expected verifier.Verify to pass, got: %v", err)
		}
	})

	t.Run("Current Unix milliseconds timestamp passes", func(t *testing.T) {
		milliTS := strconv.FormatInt(now.UnixMilli(), 10)
		milliSig := notifications.ComputeWebhookSignature(secret, milliTS, payload)
		if err := notifications.VerifyWebhook(secret, milliTS, milliSig, payload); err != nil {
			t.Fatalf("expected millisecond timestamp to pass, got: %v", err)
		}
	})

	t.Run("Current RFC3339 timestamp passes", func(t *testing.T) {
		rfcTS := now.Format(time.RFC3339)
		rfcSig := notifications.ComputeWebhookSignature(secret, rfcTS, payload)
		if err := notifications.VerifyWebhook(secret, rfcTS, rfcSig, payload); err != nil {
			t.Fatalf("expected RFC3339 timestamp to pass, got: %v", err)
		}
	})

	t.Run("WebhookVerifier with custom tolerance passes", func(t *testing.T) {
		verifier := notifications.NewWebhookVerifier(secret, 10*time.Minute)
		if err := verifier.Verify(currentTS, sig, payload); err != nil {
			t.Fatalf("expected verifier.Verify to pass, got: %v", err)
		}
	})
}

func TestWebhook_TamperedSignature(t *testing.T) {
	secret := "secure-signing-secret"
	payload := []byte(`{"event":"payment.received","amount":5000}`)
	currentTS := strconv.FormatInt(time.Now().Unix(), 10)
	validSig := notifications.ComputeWebhookSignature(secret, currentTS, payload)

	t.Run("Tampered first byte of signature fails", func(t *testing.T) {
		tamperedSig := "v1=0" + validSig[4:]
		if len(validSig) > 3 && validSig[3] == '0' {
			tamperedSig = "v1=1" + validSig[4:]
		}

		err := notifications.VerifyWebhook(secret, currentTS, tamperedSig, payload)
		if !errors.Is(err, notifications.ErrInvalidSignature) {
			t.Fatalf("expected ErrInvalidSignature, got: %v", err)
		}
	})

	t.Run("Tampered middle byte of signature fails", func(t *testing.T) {
		mid := len(validSig) / 2
		replacement := "a"
		if validSig[mid:mid+1] == "a" {
			replacement = "b"
		}
		tamperedSig := validSig[:mid] + replacement + validSig[mid+1:]

		err := notifications.VerifyWebhook(secret, currentTS, tamperedSig, payload)
		if !errors.Is(err, notifications.ErrInvalidSignature) {
			t.Fatalf("expected ErrInvalidSignature, got: %v", err)
		}
	})

	t.Run("Tampered last byte of signature fails", func(t *testing.T) {
		lastChar := validSig[len(validSig)-1]
		replacement := "0"
		if lastChar == '0' {
			replacement = "1"
		}
		tamperedSig := validSig[:len(validSig)-1] + replacement

		err := notifications.VerifyWebhook(secret, currentTS, tamperedSig, payload)
		if !errors.Is(err, notifications.ErrInvalidSignature) {
			t.Fatalf("expected ErrInvalidSignature, got: %v", err)
		}
	})

	t.Run("Tampered payload fails", func(t *testing.T) {
		tamperedPayload := []byte(`{"event":"payment.received","amount":9999}`)
		err := notifications.VerifyWebhook(secret, currentTS, validSig, tamperedPayload)
		if !errors.Is(err, notifications.ErrInvalidSignature) {
			t.Fatalf("expected ErrInvalidSignature, got: %v", err)
		}
	})

	t.Run("Wrong secret fails", func(t *testing.T) {
		err := notifications.VerifyWebhook("wrong-secret", currentTS, validSig, payload)
		if !errors.Is(err, notifications.ErrInvalidSignature) {
			t.Fatalf("expected ErrInvalidSignature, got: %v", err)
		}
	})

	t.Run("Empty secret fails", func(t *testing.T) {
		err := notifications.VerifyWebhook("", currentTS, validSig, payload)
		if !errors.Is(err, notifications.ErrInvalidSignature) {
			t.Fatalf("expected ErrInvalidSignature, got: %v", err)
		}
	})

	t.Run("Empty signature fails", func(t *testing.T) {
		err := notifications.VerifyWebhook(secret, currentTS, "", payload)
		if !errors.Is(err, notifications.ErrInvalidSignature) {
			t.Fatalf("expected ErrInvalidSignature, got: %v", err)
		}
	})

	t.Run("Truncated and extended signatures fail", func(t *testing.T) {
		truncated := validSig[:len(validSig)-4]
		if err := notifications.VerifyWebhook(secret, currentTS, truncated, payload); !errors.Is(err, notifications.ErrInvalidSignature) {
			t.Fatalf("expected truncated signature to fail with ErrInvalidSignature, got: %v", err)
		}

		extended := validSig + "abcd"
		if err := notifications.VerifyWebhook(secret, currentTS, extended, payload); !errors.Is(err, notifications.ErrInvalidSignature) {
			t.Fatalf("expected extended signature to fail with ErrInvalidSignature, got: %v", err)
		}
	})

	t.Run("Invalid prefix fails", func(t *testing.T) {
		noPrefix := strings.TrimPrefix(validSig, "v1=")
		if err := notifications.VerifyWebhook(secret, currentTS, noPrefix, payload); !errors.Is(err, notifications.ErrInvalidSignature) {
			t.Fatalf("expected signature without v1= prefix to fail with ErrInvalidSignature, got: %v", err)
		}

		sha512Prefix := "sha512=" + noPrefix
		if err := notifications.VerifyWebhook(secret, currentTS, sha512Prefix, payload); !errors.Is(err, notifications.ErrInvalidSignature) {
			t.Fatalf("expected signature with sha512= prefix to fail with ErrInvalidSignature, got: %v", err)
		}
	})
}

func TestWebhook_ExpiredAndReplayedTimestamp(t *testing.T) {
	secret := "replay-test-secret"
	payload := []byte(`{"action":"funds.transfer","amount":10000}`)
	now := time.Now()

	t.Run("Replayed request with expired timestamp (6 minutes ago) fails", func(t *testing.T) {
		expiredTS := strconv.FormatInt(now.Add(-6*time.Minute).Unix(), 10)
		sig := notifications.ComputeWebhookSignature(secret, expiredTS, payload)

		err := notifications.VerifyWebhook(secret, expiredTS, sig, payload)
		if !errors.Is(err, notifications.ErrTimestampExpired) {
			t.Fatalf("expected ErrTimestampExpired on replayed webhook, got: %v", err)
		}

		// Passes when custom tolerance covers the timestamp
		verifier10m := notifications.NewWebhookVerifier(secret, 10*time.Minute)
		if err := verifier10m.Verify(expiredTS, sig, payload); err != nil {
			t.Fatalf("expected verification to pass with 10-minute tolerance, got: %v", err)
		}
	})

	t.Run("Replay attack with refreshed timestamp header fails HMAC signature", func(t *testing.T) {
		oldTS := strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10)
		capturedSig := notifications.ComputeWebhookSignature(secret, oldTS, payload)

		refreshedTS := strconv.FormatInt(now.Unix(), 10)
		err := notifications.VerifyWebhook(secret, refreshedTS, capturedSig, payload)
		if !errors.Is(err, notifications.ErrInvalidSignature) {
			t.Fatalf("expected ErrInvalidSignature when timestamp in header doesn't match HMAC binding, got: %v", err)
		}
	})

	t.Run("Replayed request from days ago fails", func(t *testing.T) {
		oldTS := strconv.FormatInt(now.Add(-72*time.Hour).Unix(), 10)
		sig := notifications.ComputeWebhookSignature(secret, oldTS, payload)
		err := notifications.VerifyWebhook(secret, oldTS, sig, payload)
		if !errors.Is(err, notifications.ErrTimestampExpired) {
			t.Fatalf("expected ErrTimestampExpired for old timestamp, got: %v", err)
		}
	})

	t.Run("Future timestamp beyond tolerance fails", func(t *testing.T) {
		futureTS := strconv.FormatInt(now.Add(10*time.Minute).Unix(), 10)
		sig := notifications.ComputeWebhookSignature(secret, futureTS, payload)
		err := notifications.VerifyWebhook(secret, futureTS, sig, payload)
		if !errors.Is(err, notifications.ErrTimestampExpired) {
			t.Fatalf("expected ErrTimestampExpired for future timestamp, got: %v", err)
		}
	})

	t.Run("Configurable tolerance windows", func(t *testing.T) {
		shortTolerance := 30 * time.Second
		ts20sAgo := strconv.FormatInt(now.Add(-20*time.Second).Unix(), 10)
		sig20s := notifications.ComputeWebhookSignature(secret, ts20sAgo, payload)
		ts45sAgo := strconv.FormatInt(now.Add(-45*time.Second).Unix(), 10)
		sig45s := notifications.ComputeWebhookSignature(secret, ts45sAgo, payload)

		vShort := notifications.NewWebhookVerifier(secret, shortTolerance)
		if err := vShort.Verify(ts20sAgo, sig20s, payload); err != nil {
			t.Fatalf("expected 20s ago to pass 30s tolerance, got: %v", err)
		}
		if err := vShort.Verify(ts45sAgo, sig45s, payload); !errors.Is(err, notifications.ErrTimestampExpired) {
			t.Fatalf("expected 45s ago to fail 30s tolerance, got: %v", err)
		}

		longTolerance := 15 * time.Minute
		ts10mAgo := strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10)
		sig10m := notifications.ComputeWebhookSignature(secret, ts10mAgo, payload)
		ts20mAgo := strconv.FormatInt(now.Add(-20*time.Minute).Unix(), 10)
		sig20m := notifications.ComputeWebhookSignature(secret, ts20mAgo, payload)

		vLong := notifications.NewWebhookVerifier(secret, longTolerance)
		if err := vLong.Verify(ts10mAgo, sig10m, payload); err != nil {
			t.Fatalf("expected 10m ago to pass 15m tolerance, got: %v", err)
		}
		if err := vLong.Verify(ts20mAgo, sig20m, payload); !errors.Is(err, notifications.ErrTimestampExpired) {
			t.Fatalf("expected 20m ago to fail 15m tolerance, got: %v", err)
		}
	})

	t.Run("Invalid timestamp formats fail", func(t *testing.T) {
		sig := notifications.ComputeWebhookSignature(secret, "invalid-timestamp", payload)
		err := notifications.VerifyWebhook(secret, "invalid-timestamp", sig, payload)
		if !errors.Is(err, notifications.ErrInvalidTimestamp) {
			t.Fatalf("expected ErrInvalidTimestamp, got: %v", err)
		}

		err = notifications.VerifyWebhook(secret, "", sig, payload)
		if !errors.Is(err, notifications.ErrMissingTimestamp) {
			t.Fatalf("expected ErrMissingTimestamp, got: %v", err)
		}

		err = notifications.VerifyWebhook(secret, "   ", sig, payload)
		if !errors.Is(err, notifications.ErrMissingTimestamp) {
			t.Fatalf("expected ErrMissingTimestamp, got: %v", err)
		}
	})
}

func TestWebhook_TimingSafeComparison(t *testing.T) {
	secret := "constant-time-test-key"
	payload := []byte(`{"audit":"security_test","iteration":1}`)
	currentTS := strconv.FormatInt(time.Now().Unix(), 10)
	validSig := notifications.ComputeWebhookSignature(secret, currentTS, payload)

	t.Run("Verifies against signature mismatches across positions", func(t *testing.T) {
		for i := 0; i < len(validSig); i++ {
			tampered := []byte(validSig)
			if tampered[i] == 'a' {
				tampered[i] = 'b'
			} else {
				tampered[i] = 'a'
			}

			if err := notifications.VerifyWebhook(secret, currentTS, string(tampered), payload); err == nil {
				t.Fatalf("comparison succeeded unexpectedly for mismatch at index %d", i)
			}
		}
	})

	t.Run("Different length signatures return error safely", func(t *testing.T) {
		for delta := 1; delta < 20; delta++ {
			if delta < len(validSig) {
				shorter := validSig[:len(validSig)-delta]
				if err := notifications.VerifyWebhook(secret, currentTS, shorter, payload); err == nil {
					t.Fatalf("shorter signature succeeded unexpectedly")
				}
			}

			longer := validSig + strings.Repeat("x", delta)
			if err := notifications.VerifyWebhook(secret, currentTS, longer, payload); err == nil {
				t.Fatalf("longer signature succeeded unexpectedly")
			}
		}
	})

	t.Run("Exact match returns nil", func(t *testing.T) {
		if err := notifications.VerifyWebhook(secret, currentTS, validSig, payload); err != nil {
			t.Fatalf("expected exact match to succeed, got: %v", err)
		}
	})

	t.Run("Concurrent timing-safe verification under race detector", func(t *testing.T) {
		var wg sync.WaitGroup
		workers := 16
		iterations := 100

		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				for i := 0; i < iterations; i++ {
					if err := notifications.VerifyWebhook(secret, currentTS, validSig, payload); err != nil {
						t.Errorf("worker %d: valid signature check failed: %v", workerID, err)
					}

					invalidSig := validSig[:len(validSig)-1] + "z"
					if err := notifications.VerifyWebhook(secret, currentTS, invalidSig, payload); err == nil {
						t.Errorf("worker %d: invalid signature check unexpectedly passed", workerID)
					}
				}
			}(w)
		}

		wg.Wait()
	})
}

func TestWebhook_BackwardsCompatibility(t *testing.T) {
	secret := "legacy-secret-key-12345"
	payload := []byte(`{"event":"invoice.created","invoice_id":"inv-999"}`)
	now := time.Now()
	currentTS := fmt.Sprintf("%d", now.Unix())

	t.Run("Empty timestamp is strictly rejected with ErrMissingTimestamp", func(t *testing.T) {
		validSig := notifications.ComputeWebhookSignature(secret, currentTS, payload)
		err := notifications.VerifyWebhook(secret, "", validSig, payload)
		if !errors.Is(err, notifications.ErrMissingTimestamp) {
			t.Fatalf("expected ErrMissingTimestamp on empty timestamp, got: %v", err)
		}
	})

	// 1. v1.2.0 intermediate format: sha256=<hmac(timestamp+"."+payload)>
	macIntermediate := hmac.New(sha256.New, []byte(secret))
	macIntermediate.Write([]byte(currentTS + "."))
	macIntermediate.Write(payload)
	intermediateHex := hex.EncodeToString(macIntermediate.Sum(nil))
	intermediateSig := "sha256=" + intermediateHex

	t.Run("v1.2.0 intermediate sha256= prefix passes VerifyWebhook", func(t *testing.T) {
		err := notifications.VerifyWebhook(secret, currentTS, intermediateSig, payload)
		if err != nil {
			t.Fatalf("expected v1.2.0 intermediate signature to pass, got: %v", err)
		}
	})

	// 2. Tagged signature: t=<ts>,v1=<hex>
	v1Hex := notifications.ComputeWebhookSignature(secret, currentTS, payload)
	sigHex := strings.TrimPrefix(v1Hex, "v1=")
	taggedSig := fmt.Sprintf("t=%s,v1=%s", currentTS, sigHex)

	t.Run("Tagged signature format t=...,v1=... passes VerifyWebhook", func(t *testing.T) {
		err := notifications.VerifyWebhook(secret, "", taggedSig, payload)
		if err != nil {
			t.Fatalf("expected tagged signature to pass, got: %v", err)
		}
	})

	t.Run("WebhookVerifier struct methods verify correctly", func(t *testing.T) {
		verifier := notifications.NewWebhookVerifier(secret, 5*time.Minute)

		if err := verifier.Verify(currentTS, v1Hex, payload); err != nil {
			t.Fatalf("expected verifier.Verify to pass, got: %v", err)
		}

		if err := verifier.Verify(currentTS, "invalid-sig", payload); !errors.Is(err, notifications.ErrInvalidSignature) {
			t.Fatalf("expected verifier.Verify with invalid sig to fail, got: %v", err)
		}

		// Test default tolerance when non-positive
		verifierDefault := notifications.NewWebhookVerifier(secret, 0)
		if verifierDefault.Tolerance != notifications.DefaultWebhookTolerance {
			t.Fatalf("expected tolerance to default to 5m, got %v", verifierDefault.Tolerance)
		}
		if err := verifierDefault.Verify(currentTS, v1Hex, payload); err != nil {
			t.Fatalf("expected verifierDefault.Verify to pass, got: %v", err)
		}
	})
}

func TestWebhook_ReplayRejectionTable(t *testing.T) {
	secret := "replay-table-secret-key-123"
	payload := []byte(`{"event":"payment.completed","id":"tx-12345"}`)
	now := time.Now()

	tests := []struct {
		name        string
		offset      time.Duration
		tolerance   time.Duration
		expectError bool
		errType     error
	}{
		{
			name:        "Current timestamp within 5m tolerance passes",
			offset:      0,
			tolerance:   5 * time.Minute,
			expectError: false,
		},
		{
			name:        "Timestamp 4 minutes ago within 5m tolerance passes",
			offset:      -4 * time.Minute,
			tolerance:   5 * time.Minute,
			expectError: false,
		},
		{
			name:        "Timestamp 5m1s ago outside 5m tolerance fails",
			offset:      -5*time.Minute - time.Second,
			tolerance:   5 * time.Minute,
			expectError: true,
			errType:     notifications.ErrTimestampExpired,
		},
		{
			name:        "Timestamp 10 minutes ago outside 5m tolerance fails",
			offset:      -10 * time.Minute,
			tolerance:   5 * time.Minute,
			expectError: true,
			errType:     notifications.ErrTimestampExpired,
		},
		{
			name:        "Timestamp 1 hour ago outside 5m tolerance fails",
			offset:      -1 * time.Hour,
			tolerance:   5 * time.Minute,
			expectError: true,
			errType:     notifications.ErrTimestampExpired,
		},
		{
			name:        "Future timestamp 6 minutes ahead outside 5m tolerance fails",
			offset:      6 * time.Minute,
			tolerance:   5 * time.Minute,
			expectError: true,
			errType:     notifications.ErrTimestampExpired,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tsStr := strconv.FormatInt(now.Add(tc.offset).Unix(), 10)
			sig := notifications.ComputeWebhookSignature(secret, tsStr, payload)

			// 1. VerifyWebhook (uses DefaultWebhookTolerance = 5m)
			err1 := notifications.VerifyWebhook(secret, tsStr, sig, payload)
			if tc.expectError {
				if !errors.Is(err1, tc.errType) {
					t.Errorf("VerifyWebhook: expected error %v, got %v", tc.errType, err1)
				}
			} else {
				if err1 != nil {
					t.Errorf("VerifyWebhook: expected success, got %v", err1)
				}
			}

			// 2. WebhookVerifier.Verify
			verifier := notifications.NewWebhookVerifier(secret, tc.tolerance)
			err2 := verifier.Verify(tsStr, sig, payload)
			if tc.expectError {
				if !errors.Is(err2, tc.errType) {
					t.Errorf("WebhookVerifier.Verify: expected error %v, got %v", tc.errType, err2)
				}
			} else {
				if err2 != nil {
					t.Errorf("WebhookVerifier.Verify: expected success, got %v", err2)
				}
			}
		})
	}
}
