package notifications

import (
	"context"
	"time"
)

// Channel identifies a communication medium.
type Channel string

const (
	ChannelEmail   Channel = "EMAIL"
	ChannelSlack   Channel = "SLACK"
	ChannelWebhook Channel = "WEBHOOK"
	ChannelInApp   Channel = "IN_APP"
)

// Priority defines the urgency of a notification.
type Priority string

const (
	PriorityLow      Priority = "LOW"
	PriorityNormal   Priority = "NORMAL"
	PriorityHigh     Priority = "HIGH"
	PriorityCritical Priority = "CRITICAL"
)

// Attachment represents a file or document to be delivered alongside the message.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// Message encapsulates the notification payload and dispatch directives.
type Message struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Body        string            `json:"body"`
	HTMLBody    string            `json:"html_body,omitempty"`
	Priority    Priority          `json:"priority"`
	Channels    []Channel         `json:"channels"`
	Recipients  []string          `json:"recipients"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Attachments []Attachment      `json:"-"`
	CreatedAt   time.Time         `json:"created_at"`
}

// Sender is the interface implemented by each channel adapter (Email, Slack, Webhook, etc.).
type Sender interface {
	Channel() Channel
	Send(ctx context.Context, msg Message) error
}
