package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

var (
	ErrMissingStore         = errors.New("outbox store is required")
	ErrMissingPublisher     = errors.New("outbox publisher is required")
	ErrInvalidLeaseDuration = errors.New("outbox lease duration must be >= 5s and strictly greater than publish timeout")
)

// Publisher publishes an event to an external broker (Kafka, RabbitMQ, SQS, Webhook, etc.).
type Publisher interface {
	Publish(ctx context.Context, event Event) error
}

// PublisherFunc allows using an inline function as a Publisher.
type PublisherFunc func(ctx context.Context, event Event) error

func (f PublisherFunc) Publish(ctx context.Context, event Event) error {
	return f(ctx, event)
}

// RelayConfig configures the outbox poller and dispatcher engine.
type RelayConfig struct {
	BatchSize      int
	PollInterval   time.Duration
	PublishTimeout time.Duration // Bounded timeout for individual event publish (default: 5s)
	LeaseDuration  time.Duration // Worker lease lock duration (default: 60s, minimum: 5s, must exceed PublishTimeout)
	BackoffBase    time.Duration
	BackoffMax     time.Duration
	Concurrency    int // Number of parallel poller worker goroutines (default: 1)
	Store          Store
	Publisher      Publisher
	Logger         *slog.Logger
}

// Relay executes polling and reliable event delivery.
type Relay struct {
	cfg RelayConfig
}

func validateConfig(cfg *RelayConfig) error {
	if cfg.Store == nil {
		return ErrMissingStore
	}
	if cfg.Publisher == nil {
		return ErrMissingPublisher
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 1 * time.Second
	}
	if cfg.PublishTimeout <= 0 {
		cfg.PublishTimeout = 5 * time.Second
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = 60 * time.Second
	}
	if cfg.LeaseDuration < 5*time.Second || cfg.LeaseDuration <= cfg.PublishTimeout {
		return fmt.Errorf("%w: lease_duration=%v, publish_timeout=%v", ErrInvalidLeaseDuration, cfg.LeaseDuration, cfg.PublishTimeout)
	}
	if cfg.BackoffBase <= 0 {
		cfg.BackoffBase = 2 * time.Second
	}
	if cfg.BackoffMax <= 0 {
		cfg.BackoffMax = 1 * time.Minute
	}
	if cfg.BackoffMax < cfg.BackoffBase {
		cfg.BackoffMax = cfg.BackoffBase
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return nil
}

// NewRelay instantiates an outbox relay engine.
func NewRelay(cfg RelayConfig) (*Relay, error) {
	if err := validateConfig(&cfg); err != nil {
		return nil, err
	}
	return &Relay{cfg: cfg}, nil
}

// ProcessBatch retrieves and dispatches a single batch of events.
// Returns the count of processed events in this iteration.
func (r *Relay) ProcessBatch(ctx context.Context) (int, error) {
	events, err := r.cfg.Store.FetchPendingBatch(ctx, r.cfg.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("relay batch fetch failed: %w", err)
	}

	if len(events) == 0 {
		return 0, nil
	}

	processedCount := 0
	for _, event := range events {
		select {
		case <-ctx.Done():
			return processedCount, ctx.Err()
		default:
		}

		// Bound event publish context to PublishTimeout to prevent hanging goroutines and double delivery
		if event.LockedUntil != nil && time.Until(*event.LockedUntil) <= 0 {
			r.cfg.Logger.Warn("outbox event lease already expired before publish", "id", event.ID)
			continue
		}

		pubCtx, cancel := context.WithTimeout(ctx, r.cfg.PublishTimeout)
		pubErr := r.cfg.Publisher.Publish(pubCtx, event)
		cancel()
		if pubErr == nil {
			var markErr error
			if event.LeaseToken != nil {
				markErr = r.cfg.Store.MarkPublished(ctx, event.ID, *event.LeaseToken)
			} else {
				markErr = r.cfg.Store.MarkPublished(ctx, event.ID)
			}
			if markErr != nil {
				r.cfg.Logger.Error("failed to mark outbox event published", "id", event.ID, "error", markErr)
				return processedCount, fmt.Errorf("failed to mark outbox event %s as published: %w", event.ID, markErr)
			}
			processedCount++
			continue
		}

		// Handle failure & exponential backoff calculation
		newRetryCount := event.RetryCount + 1
		isPoisonPill := IsNonRetryable(pubErr)
		finalFail := newRetryCount >= event.MaxRetries || isPoisonPill

		maxDelay := r.cfg.BackoffMax
		if maxDelay <= 0 {
			maxDelay = 1 * time.Minute
		}
		if maxDelay < r.cfg.BackoffBase {
			maxDelay = r.cfg.BackoffBase
		}
		backoff := BackoffWithJitter(event.RetryCount, r.cfg.BackoffBase, maxDelay)
		nextRetry := time.Now().UTC().Add(backoff)

		r.cfg.Logger.Warn("outbox event publish failed",
			"id", event.ID,
			"event_type", event.EventType,
			"retry_count", newRetryCount,
			"max_retries", event.MaxRetries,
			"final_fail", finalFail,
			"poison_pill", isPoisonPill,
			"backoff", backoff,
			"error", pubErr,
		)

		var failErr error
		if event.LeaseToken != nil {
			failErr = r.cfg.Store.MarkFailed(ctx, event.ID, pubErr.Error(), nextRetry, finalFail, *event.LeaseToken)
		} else {
			failErr = r.cfg.Store.MarkFailed(ctx, event.ID, pubErr.Error(), nextRetry, finalFail)
		}
		if failErr != nil {
			r.cfg.Logger.Error("failed to record outbox failure state", "id", event.ID, "error", failErr)
			return processedCount, fmt.Errorf("failed to record failure state for outbox event %s: %w", event.ID, failErr)
		}
		processedCount++
	}

	return processedCount, nil
}

// Start begins the polling loop, blocking until the context is canceled.
// When Concurrency > 1, it spawns multiple concurrent worker loops.
func (r *Relay) Start(ctx context.Context) error {
	r.cfg.Logger.Info("outbox relay poller started",
		"poll_interval", r.cfg.PollInterval,
		"batch_size", r.cfg.BatchSize,
		"concurrency", r.cfg.Concurrency,
	)

	var wg sync.WaitGroup
	for i := 0; i < r.cfg.Concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			r.runWorker(ctx, workerID)
		}(i)
	}

	<-ctx.Done()
	r.cfg.Logger.Info("outbox relay poller stopping")
	wg.Wait()
	return ctx.Err()
}

func (r *Relay) runWorker(ctx context.Context, workerID int) {
	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			count, err := r.ProcessBatch(ctx)
			if err != nil && !errors.Is(err, context.Canceled) {
				r.cfg.Logger.Error("error processing outbox batch", "worker_id", workerID, "error", err)
			}
			// If we processed a full batch, immediately check for another batch without waiting
			if count == r.cfg.BatchSize {
				for count == r.cfg.BatchSize {
					if ctx.Err() != nil {
						return
					}
					var err error
					count, err = r.ProcessBatch(ctx)
					if err != nil {
						if !errors.Is(err, context.Canceled) {
							r.cfg.Logger.Error("error processing outbox batch during worker drain", "worker_id", workerID, "error", err)
						}
						break
					}
				}
			}
		}
	}
}
