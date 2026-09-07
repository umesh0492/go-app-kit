package notifications

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/umesh0492/go-libs/workerpool"
)

var (
	ErrNoSenderRegistered = errors.New("no sender registered for channel")
	ErrEmptyRecipients    = errors.New("message recipients cannot be empty")
	ErrBrokerClosed       = errors.New("notification broker is closed")
)

// Broker coordinates multi-channel message dispatching.
type Broker interface {
	RegisterSender(sender Sender)
	Send(ctx context.Context, msg Message) error
	SendAsync(ctx context.Context, msg Message) error
	Close()
}

// Config configures the notification broker.
type Config struct {
	Workers   int
	QueueSize int
	Logger    *slog.Logger
}

// DefaultConfig returns default pool dimensions for background notification delivery.
func DefaultConfig() Config {
	return Config{
		Workers:   4,
		QueueSize: 100,
		Logger:    slog.Default(),
	}
}

type defaultBroker struct {
	mu      sync.RWMutex
	senders map[Channel]Sender
	pool    *workerpool.Pool
	logger  *slog.Logger
	closed  bool
}

// NewBroker creates a new notification broker powered by a bounded worker pool.
func NewBroker(cfg Config) Broker {
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 100
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	p := workerpool.New(cfg.Workers, cfg.QueueSize, workerpool.WithLogger(cfg.Logger))

	return &defaultBroker{
		senders: make(map[Channel]Sender),
		pool:    p,
		logger:  cfg.Logger,
	}
}

// RegisterSender binds a channel sender adapter to the broker.
func (b *defaultBroker) RegisterSender(sender Sender) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.senders[sender.Channel()] = sender
}

// Send synchronously dispatches the message to all configured channels.
func (b *defaultBroker) Send(ctx context.Context, msg Message) error {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return ErrBrokerClosed
	}
	b.mu.RUnlock()

	if len(msg.Recipients) == 0 {
		return ErrEmptyRecipients
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}

	channels := msg.Channels
	if len(channels) == 0 {
		// Default to all registered channels
		b.mu.RLock()
		for ch := range b.senders {
			channels = append(channels, ch)
		}
		b.mu.RUnlock()
	}

	var errs []error
	for _, ch := range channels {
		b.mu.RLock()
		sender, ok := b.senders[ch]
		b.mu.RUnlock()

		if !ok {
			errs = append(errs, fmt.Errorf("%w: %s", ErrNoSenderRegistered, ch))
			continue
		}

		if err := sender.Send(ctx, msg); err != nil {
			errs = append(errs, fmt.Errorf("channel %s failed: %w", ch, err))
		}
	}

	return errors.Join(errs...)
}

// SendAsync enqueues the message for non-blocking background dispatching via the worker pool.
func (b *defaultBroker) SendAsync(ctx context.Context, msg Message) error {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return ErrBrokerClosed
	}
	b.mu.RUnlock()

	return b.pool.Submit(func() {
		// Use a detached background context with timeout for background dispatch
		dispatchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := b.Send(dispatchCtx, msg); err != nil {
			b.logger.Error("failed to dispatch async notification",
				"id", msg.ID,
				"title", msg.Title,
				"error", err,
			)
		}
	})
}

// Close gracefully stops the worker pool and shuts down the broker.
func (b *defaultBroker) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	b.mu.Unlock()

	b.pool.StopWait()
}
