package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTPClient represents a minimal HTTP client interface to allow mock testing.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// SlackConfig configures the Slack incoming webhook adapter.
type SlackConfig struct {
	WebhookURL string
	Username   string
	IconEmoji  string
	HTTPClient HTTPClient
}

type slackSender struct {
	cfg SlackConfig
}

// NewSlackSender creates a Slack notification sender.
func NewSlackSender(cfg SlackConfig) Sender {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &slackSender{cfg: cfg}
}

func (s *slackSender) Channel() Channel {
	return ChannelSlack
}

func (s *slackSender) Send(ctx context.Context, msg Message) error {
	if s.cfg.WebhookURL == "" {
		return fmt.Errorf("slack webhook URL cannot be empty")
	}

	color := "#3b82f6" // default blue
	switch msg.Priority {
	case PriorityCritical:
		color = "#ef4444" // red
	case PriorityHigh:
		color = "#f97316" // orange
	case PriorityLow:
		color = "#10b981" // green
	}

	type slackField struct {
		Title string `json:"title"`
		Value string `json:"value"`
		Short bool   `json:"short"`
	}

	type slackAttachment struct {
		Color  string       `json:"color"`
		Title  string       `json:"title"`
		Text   string       `json:"text"`
		Fields []slackField `json:"fields,omitempty"`
		Footer string       `json:"footer"`
		Ts     int64        `json:"ts"`
	}

	type slackPayload struct {
		Username    string            `json:"username,omitempty"`
		IconEmoji   string            `json:"icon_emoji,omitempty"`
		Text        string            `json:"text"`
		Attachments []slackAttachment `json:"attachments"`
	}

	fields := []slackField{
		{Title: "Priority", Value: string(msg.Priority), Short: true},
	}
	for k, v := range msg.Metadata {
		fields = append(fields, slackField{Title: k, Value: v, Short: true})
	}

	payload := slackPayload{
		Username:  s.cfg.Username,
		IconEmoji: s.cfg.IconEmoji,
		Text:      fmt.Sprintf("*%s*\n%s", msg.Title, msg.Body),
		Attachments: []slackAttachment{
			{
				Color:  color,
				Title:  msg.Title,
				Text:   msg.Body,
				Fields: fields,
				Footer: "go-app-kit Notification Engine",
				Ts:     msg.CreatedAt.Unix(),
			},
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to encode slack payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.WebhookURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("slack webhook post failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("slack webhook returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}
