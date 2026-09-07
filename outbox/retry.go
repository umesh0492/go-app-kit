package outbox

import (
	"encoding/json"
	"errors"
	"math"
	"math/rand/v2"
	"time"
)

// BackoffWithJitter calculates a full-jitter exponential backoff duration for a given attempt.
// Full jitter computes ceiling = min(maxDelay, baseDelay * 2^attempt) and returns a random
// duration uniformly distributed in [0, ceiling], preventing thundering-herd issues on downstream services.
func BackoffWithJitter(attempt int, baseDelay, maxDelay time.Duration) time.Duration {
	if baseDelay <= 0 {
		return 0
	}
	if attempt < 0 {
		attempt = 0
	}
	// Avoid bit-shift overflow (1 << 30 is ~1.07e9, multiplied by baseDelay can approach MaxInt64)
	if attempt > 30 {
		attempt = 30
	}

	multiplier := int64(1) << attempt
	var ceiling time.Duration
	if multiplier > math.MaxInt64/int64(baseDelay) {
		ceiling = time.Duration(math.MaxInt64)
	} else {
		ceiling = baseDelay * time.Duration(multiplier)
	}

	if maxDelay > 0 && ceiling > maxDelay {
		ceiling = maxDelay
	}

	if ceiling <= 0 {
		return 0
	}

	if int64(ceiling) == math.MaxInt64 {
		return time.Duration(rand.Int64N(math.MaxInt64))
	}
	return time.Duration(rand.Int64N(int64(ceiling) + 1))
}

// Sentinel errors representing non-retryable poison pill scenarios.
var (
	ErrNonRetryable = errors.New("outbox: non-retryable error")
	ErrPoisonPill   = errors.New("outbox: poison pill event")
)

// NonRetryableError marks an error as permanently unprocessable (poison pill),
// which directs the outbox relay poller to transition the event directly
// to StatusDeadLetter without wasting retry attempts.
type NonRetryableError struct {
	Err error
}

func (e *NonRetryableError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "non-retryable error"
}

func (e *NonRetryableError) Unwrap() error {
	return e.Err
}

func (e *NonRetryableError) NonRetryable() bool {
	return true
}

// MarkNonRetryable wraps an existing error into a NonRetryableError.
func MarkNonRetryable(err error) error {
	if err == nil {
		return nil
	}
	return &NonRetryableError{Err: err}
}

// WrapNonRetryable is an alias for MarkNonRetryable.
func WrapNonRetryable(err error) error {
	return MarkNonRetryable(err)
}

// IsNonRetryable inspects an error to determine whether it indicates a permanent
// failure (e.g. malformed JSON payloads, schema validation errors, or explicit non-retryable markers).
func IsNonRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNonRetryable) || errors.Is(err, ErrPoisonPill) {
		return true
	}
	var nr *NonRetryableError
	if errors.As(err, &nr) {
		return true
	}
	type nonRetryable interface {
		NonRetryable() bool
	}
	var nri nonRetryable
	if errors.As(err, &nri) {
		return nri.NonRetryable()
	}
	type poisonPill interface {
		IsPoisonPill() bool
	}
	var ppi poisonPill
	if errors.As(err, &ppi) {
		return ppi.IsPoisonPill()
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return true
	}
	var unmarshalTypeErr *json.UnmarshalTypeError
	return errors.As(err, &unmarshalTypeErr)
}
