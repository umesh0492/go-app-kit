package notifications

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"net/smtp"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SendMailFunc defines the signature of net/smtp.SendMail for test mockability.
type SendMailFunc func(addr string, a smtp.Auth, from string, to []string, msg []byte) error

// EmailConfig configures the SMTP email sender.
type EmailConfig struct {
	Host         string
	Port         int
	Username     string
	Password     string
	From         string
	FromName     string
	SendMailFunc SendMailFunc
}

type emailSender struct {
	cfg EmailConfig
}

// NewEmailSender creates an SMTP sender adapter.
func NewEmailSender(cfg EmailConfig) Sender {
	if cfg.SendMailFunc == nil {
		cfg.SendMailFunc = smtp.SendMail
	}
	return &emailSender{cfg: cfg}
}

func (s *emailSender) Channel() Channel {
	return ChannelEmail
}

func sanitizeCRLF(val string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(val)
}

func randomBoundary(prefix string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%s_%s", prefix, uuid.New().String())
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b))
}

func writeBase64(buf *bytes.Buffer, data []byte) {
	encoded := base64.StdEncoding.EncodeToString(data)
	for len(encoded) > 76 {
		buf.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	buf.WriteString(encoded + "\r\n")
}

// formatAttachmentFilename formats an attachment filename for MIME headers.
// Non-ASCII filenames (UTF-8) are encoded using RFC 2047 encoded-word syntax
// (e.g. =?UTF-8?b?...?= via mime.BEncoding) instead of Go's %q which escapes non-ASCII characters with \uXXXX.
// ASCII filenames are safely quoted with %q.
func formatAttachmentFilename(filename string) string {
	for i := 0; i < len(filename); i++ {
		if filename[i] >= 0x80 {
			return mime.BEncoding.Encode("UTF-8", filename)
		}
	}
	return fmt.Sprintf("%q", filename)
}

func (s *emailSender) Send(ctx context.Context, msg Message) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Sanitize recipients for SMTP envelope and headers to prevent CRLF injection
	cleanRecipients := make([]string, 0, len(msg.Recipients))
	for _, r := range msg.Recipients {
		clean := strings.TrimSpace(sanitizeCRLF(r))
		if clean != "" {
			cleanRecipients = append(cleanRecipients, clean)
		}
	}
	msg.Recipients = cleanRecipients

	body := s.buildMIMEMessage(msg)

	var auth smtp.Auth
	if s.cfg.Username != "" && s.cfg.Password != "" {
		auth = smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
	}

	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	return s.cfg.SendMailFunc(addr, auth, s.cfg.From, cleanRecipients, body)
}

func (s *emailSender) buildMIMEMessage(msg Message) []byte {
	var buf bytes.Buffer

	fromHeader := s.cfg.From
	if s.cfg.FromName != "" {
		fromHeader = fmt.Sprintf("%s <%s>", mime.QEncoding.Encode("UTF-8", sanitizeCRLF(s.cfg.FromName)), sanitizeCRLF(s.cfg.From))
	}

	cleanRecipients := make([]string, 0, len(msg.Recipients))
	for _, r := range msg.Recipients {
		clean := strings.TrimSpace(sanitizeCRLF(r))
		if clean != "" {
			cleanRecipients = append(cleanRecipients, clean)
		}
	}

	cleanTitle := sanitizeCRLF(msg.Title)

	// RFC 5322 mandatory headers
	fmt.Fprintf(&buf, "From: %s\r\n", fromHeader)
	fmt.Fprintf(&buf, "To: %s\r\n", strings.Join(cleanRecipients, ", "))
	fmt.Fprintf(&buf, "Subject: %s\r\n", mime.QEncoding.Encode("UTF-8", cleanTitle))

	msgDate := msg.CreatedAt
	if msgDate.IsZero() {
		msgDate = time.Now()
	}
	fmt.Fprintf(&buf, "Date: %s\r\n", msgDate.Format(time.RFC1123Z))

	hostname := s.cfg.Host
	if hostname == "" {
		if h, err := os.Hostname(); err == nil && h != "" {
			hostname = h
		} else {
			hostname = "localhost"
		}
	}
	hostname = sanitizeCRLF(hostname)
	fmt.Fprintf(&buf, "Message-ID: <%s@%s>\r\n", uuid.New().String(), hostname)

	buf.WriteString("MIME-Version: 1.0\r\n")

	mixedBoundary := randomBoundary("boundary_mixed")
	altBoundary := randomBoundary("boundary_alt")

	hasAttachments := len(msg.Attachments) > 0

	if hasAttachments {
		fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=%s\r\n\r\n", mixedBoundary)
		fmt.Fprintf(&buf, "--%s\r\n", mixedBoundary)
	}

	if msg.HTMLBody != "" {
		fmt.Fprintf(&buf, "Content-Type: multipart/alternative; boundary=%s\r\n\r\n", altBoundary)

		// Plain text part (Base64 transfer encoding for UTF-8)
		fmt.Fprintf(&buf, "--%s\r\n", altBoundary)
		buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		buf.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		writeBase64(&buf, []byte(msg.Body))
		buf.WriteString("\r\n")

		// HTML part (Base64 transfer encoding for UTF-8)
		fmt.Fprintf(&buf, "--%s\r\n", altBoundary)
		buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
		buf.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		writeBase64(&buf, []byte(msg.HTMLBody))
		buf.WriteString("\r\n")

		fmt.Fprintf(&buf, "--%s--\r\n", altBoundary)
	} else {
		buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		buf.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		writeBase64(&buf, []byte(msg.Body))
		buf.WriteString("\r\n")
	}

	// Attachments
	for _, att := range msg.Attachments {
		fmt.Fprintf(&buf, "\r\n--%s\r\n", mixedBoundary)
		contentType := sanitizeCRLF(att.ContentType)
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		cleanFilename := sanitizeCRLF(att.Filename)
		formattedFilename := formatAttachmentFilename(cleanFilename)
		fmt.Fprintf(&buf, "Content-Type: %s; name=%s\r\n", contentType, formattedFilename)
		buf.WriteString("Content-Transfer-Encoding: base64\r\n")
		fmt.Fprintf(&buf, "Content-Disposition: attachment; filename=%s\r\n\r\n", formattedFilename)

		writeBase64(&buf, att.Data)
	}

	if hasAttachments {
		fmt.Fprintf(&buf, "\r\n--%s--\r\n", mixedBoundary)
	}

	return buf.Bytes()
}
