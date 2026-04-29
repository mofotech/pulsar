package retry

import (
	"context"
	"math/rand"
	"time"
)

// Do retries fn with exponential backoff + jitter until it returns nil,
// ctx is cancelled, or maxAttempts is exhausted.
func Do(ctx context.Context, maxAttempts int, baseDelay time.Duration, fn func() error) error {
	var err error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err = fn(); err == nil {
			return nil
		}
		if attempt == maxAttempts-1 {
			break
		}
		delay := backoff(baseDelay, attempt)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func backoff(base time.Duration, attempt int) time.Duration {
	exp := base * (1 << attempt)
	jitter := time.Duration(rand.Int63n(int64(exp) / 2)) //nolint:gosec // jitter doesn't need cryptographic randomness
	return exp + jitter
}
