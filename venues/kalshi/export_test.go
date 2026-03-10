// export_test.go is compiled only during `go test`. It exposes unexported
// fields to the external test package (package kalshi_test) without adding
// any surface to the production API.
package kalshi

import (
	"time"

	"golang.org/x/time/rate"
)

// SetRetryBaseDelay overrides the exponential-backoff base delay used by
// doSignedGet when retrying 429 responses. Set to 0 in tests so retries
// complete instantly without sleeping.
func SetRetryBaseDelay(c *KalshiClient, d time.Duration) {
	c.retryBaseDelay = d
}

// SetRateLimiter replaces the client's token-bucket rate limiter. Tests pass
// rate.NewLimiter(rate.Inf, 1) to allow unlimited throughput so retry and
// pagination tests are not artificially slowed by the production 60 ms token
// interval.
func SetRateLimiter(c *KalshiClient, l *rate.Limiter) {
	c.rateLimiter = l
}
