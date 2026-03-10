package kalshi

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"golang.org/x/time/rate"

	equinoxerrors "github.com/equinox/errors"
	"github.com/equinox/config"
	"github.com/equinox/logger"
	"github.com/equinox/models"
)

// KalshiClient fetches market data from the Kalshi Trade API v2, signs every
// request with RSA-PSS SHA256 credentials, and adapts raw responses into
// canonical models.Market structs.
//
// The private key is loaded once at construction time and stored on the struct;
// it is never reloaded per-request.
//
// seriesCache holds the pre-warmed index of all Kalshi series. When warm,
// FetchMarkets uses series-targeted fetching (faster) instead of a full
// catalogue scan (fallback). Call WarmSeriesCache at startup to enable this.
//
// rateLimiter enforces Kalshi's Basic-tier read limit of 20 req/s. A token
// bucket with a 60 ms interval (~16.7 req/s) provides a comfortable margin.
// All outbound requests share this single limiter via doSignedGet.
//
// retryBaseDelay is the base duration for exponential backoff when doSignedGet
// receives a 429. The delay doubles with each attempt: base, 2×base, 4×base.
type KalshiClient struct {
	httpClient     *http.Client
	baseURL        string
	apiKeyID       string
	privateKey     *rsa.PrivateKey
	log            *logger.Logger
	seriesCache    *SeriesCache
	rateLimiter    *rate.Limiter
	retryBaseDelay time.Duration
}

// NewKalshiClient constructs a KalshiClient from the provided configuration.
// It reads and parses the RSA private key at cfg.KalshiAPIKeyPath once. Both
// PKCS8 ("BEGIN PRIVATE KEY") and PKCS1 ("BEGIN RSA PRIVATE KEY") PEM formats
// are accepted. Returns an EquinoxError if the key cannot be loaded or parsed.
func NewKalshiClient(cfg *config.Config, log *logger.Logger) (*KalshiClient, error) {
	keyBytes, err := os.ReadFile(cfg.KalshiAPIKeyPath)
	if err != nil {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "kalshi",
			Message: "failed to read RSA private key file",
			Err:     err,
		}
	}

	block, _ := pem.Decode(keyBytes)
	if block == nil {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "kalshi",
			Message: "no PEM block found in private key file",
		}
	}

	rsaKey, err := parseRSAPrivateKey(block)
	if err != nil {
		return nil, err
	}

	return &KalshiClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 20 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
		},
		baseURL:        cfg.KalshiBaseURL,
		apiKeyID:       cfg.KalshiAPIKeyID,
		privateKey:     rsaKey,
		log:            log,
		seriesCache:    newSeriesCache(15 * time.Minute),
		rateLimiter:    rate.NewLimiter(rate.Every(60*time.Millisecond), 3),
		retryBaseDelay: 500 * time.Millisecond,
	}, nil
}

// parseRSAPrivateKey attempts PKCS8 first, then falls back to PKCS1.
func parseRSAPrivateKey(block *pem.Block) (*rsa.PrivateKey, error) {
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, &equinoxerrors.EquinoxError{
				Layer:   "connector",
				Venue:   "kalshi",
				Message: "PKCS8 private key is not an RSA key",
			}
		}
		return rsaKey, nil
	}

	rsaKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "kalshi",
			Message: "failed to parse private key as PKCS8 or PKCS1",
			Err:     err,
		}
	}
	return rsaKey, nil
}

// GetVenueName satisfies the VenueConnector interface.
func (c *KalshiClient) GetVenueName() string { return "kalshi" }

// degeneratePriceThreshold is the distance from 0 or 1 at which a market's
// mid price is considered effectively decided. A mid of 0.97 or 0.03 carries
// no actionable signal for equivalence detection or routing.
const degeneratePriceThreshold = 0.03

// FetchMarkets pages through all open Kalshi events via SearchAllOpenEvents,
// applies the two-layer query filter (event title, then market subtitle), and
// adapts every matching market leg into a canonical models.Market.
//
// Before adaptation, each leg is filtered through four client-side guards:
//
//  1. Status — only legs in "active" status are tradeable. Pre-trading states
//     (initialized, inactive) and post-trading states (closed, determined,
//     finalized, disputed, amended) are discarded.
//
//  2. Result — legs with a non-empty result ("yes", "no", "scalar") have
//     already been determined and are excluded.
//
//  3. Settlement — legs with a non-empty settlement_ts have been settled and
//     are excluded.
//
//  4. Degenerate price — legs whose mid price is within 0.03 of 0 or 1 are
//     effectively decided and provide no useful signal.
//
//  5. Dead market — legs with zero open interest AND zero 24h volume have no
//     activity and are excluded.
func (c *KalshiClient) FetchMarkets(ctx context.Context, query string) ([]models.Market, error) {
	c.log.Info("connector", "kalshi", fmt.Sprintf("fetching markets for query: %q", query))

	var (
		events []KalshiEvent
		err    error
	)
	if c.seriesCache.IsWarm() {
		c.log.Debug("connector", "kalshi", "series cache warm — using targeted series search")
		events, err = c.searchEventsBySeries(ctx, query)
		if err != nil {
			return nil, err
		}
	}
	// Fall back to the full catalogue scan when:
	//   a) the series cache is cold (TTL expired), or
	//   b) the cache is warm but no series scored above the relevance floor for
	//      this query (e.g. a novel topic not yet in any series title/tag).
	// This ensures every query gets a result even when the targeted path misses.
	if len(events) == 0 {
		c.log.Debug("connector", "kalshi",
			"series-targeted search returned no events — falling back to full catalogue scan")
		events, err = c.SearchAllOpenEvents(ctx, query)
		if err != nil {
			return nil, err
		}
	}

	var totalLegs, skippedStatus, skippedResult, skippedSettled int
	var skippedDegenerate, skippedDead int

	markets := make([]models.Market, 0, len(events)*2)
	for _, event := range events {
		for _, leg := range event.Markets {
			totalLegs++

			if leg.Status != "active" {
				skippedStatus++
				c.log.Debug("connector", "kalshi", fmt.Sprintf(
					"skip leg %q: status=%q (not active)", leg.Ticker, leg.Status))
				continue
			}

			if leg.Result != "" {
				skippedResult++
				c.log.Debug("connector", "kalshi", fmt.Sprintf(
					"skip leg %q: result=%q (already determined)", leg.Ticker, leg.Result))
				continue
			}

			if leg.SettlementTs != "" {
				skippedSettled++
				c.log.Debug("connector", "kalshi", fmt.Sprintf(
					"skip leg %q: settled at %s", leg.Ticker, leg.SettlementTs))
				continue
			}

			m, err := AdaptKalshiMarket(event, leg)
			if err != nil {
				c.log.Warn("connector", "kalshi",
					fmt.Sprintf("skipping market %q in event %q: %v",
						leg.Ticker, event.EventTicker, err))
				continue
			}

			if m.YesMid <= degeneratePriceThreshold || m.YesMid >= (1.0-degeneratePriceThreshold) {
				skippedDegenerate++
				c.log.Debug("connector", "kalshi", fmt.Sprintf(
					"skip leg %q: degenerate price mid=%.4f", leg.Ticker, m.YesMid))
				continue
			}

			if leg.OpenInterest == 0 && leg.Volume24h == 0 {
				skippedDead++
				c.log.Debug("connector", "kalshi", fmt.Sprintf(
					"skip leg %q: zero open interest and zero 24h volume", leg.Ticker))
				continue
			}

			markets = append(markets, m)
		}
	}

	c.log.Debug("connector", "kalshi", fmt.Sprintf(
		"leg filter stats: total=%d status=%d result=%d settled=%d degenerate=%d dead=%d",
		totalLegs, skippedStatus, skippedResult, skippedSettled, skippedDegenerate, skippedDead))
	c.log.Info("connector", "kalshi", fmt.Sprintf(
		"adapted %d markets from %d legs across %d events for query %q",
		len(markets), totalLegs, len(events), query))
	return markets, nil
}

// maxRetries is the number of times doSignedGet will retry a 429 response
// before giving up. Total attempts = 1 (initial) + maxRetries.
const maxRetries = 3

// doSignedGet signs and executes a GET request to fullURL, checks the status
// code, and returns the raw *http.Response so callers can decode the body into
// their own type. The caller is responsible for closing resp.Body.
//
// Rate limiting: each call waits for a token from the shared rateLimiter
// before firing, enforcing Kalshi's 20 req/s Basic-tier read limit.
//
// 429 handling: on TooManyRequests the request is retried up to maxRetries
// times with exponential backoff (retryBaseDelay × 2^attempt). If the
// response carries a Retry-After header its value takes precedence. After
// maxRetries exhausted, an EquinoxError is returned.
//
// doSignedGet returns an EquinoxError on any of: rate-limiter cancellation,
// request build failure, signing failure, transport error, or a non-200/429
// HTTP status.
func (c *KalshiClient) doSignedGet(ctx context.Context, fullURL string) (*http.Response, error) {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Acquire a rate-limiter token before each attempt (including retries).
		// Wait blocks until a token is available or the context is cancelled.
		if err := c.rateLimiter.Wait(ctx); err != nil {
			return nil, &equinoxerrors.EquinoxError{
				Layer:   "connector",
				Venue:   "kalshi",
				Message: "rate limiter wait cancelled",
				Err:     err,
			}
		}

		// Re-build and re-sign on every attempt: the timestamp header must be
		// fresh or Kalshi will reject the signature.
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
		if err != nil {
			return nil, &equinoxerrors.EquinoxError{
				Layer:   "connector",
				Venue:   "kalshi",
				Message: "failed to build HTTP request",
				Err:     err,
			}
		}
		req.Header.Set("Accept", "application/json")

		if err := c.signRequest(req); err != nil {
			return nil, err
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, &equinoxerrors.EquinoxError{
				Layer:   "connector",
				Venue:   "kalshi",
				Message: "HTTP request failed",
				Err:     err,
			}
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			lastErr = &equinoxerrors.EquinoxError{
				Layer:   "connector",
				Venue:   "kalshi",
				Message: fmt.Sprintf("API returned status 429 after %d attempt(s)", attempt+1),
				Err:     fmt.Errorf("status 429"),
			}
			if attempt < maxRetries {
				delay := c.retryBackoff(attempt, resp.Header.Get("Retry-After"))
				c.log.Warn("connector", "kalshi", fmt.Sprintf(
					"429 rate-limited on attempt %d/%d; retrying in %s",
					attempt+1, maxRetries+1, delay,
				))
				select {
				case <-ctx.Done():
					return nil, &equinoxerrors.EquinoxError{
						Layer:   "connector",
						Venue:   "kalshi",
						Message: "context cancelled while waiting to retry after 429",
						Err:     ctx.Err(),
					}
				case <-time.After(delay):
				}
				continue
			}
			return nil, lastErr
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, &equinoxerrors.EquinoxError{
				Layer:   "connector",
				Venue:   "kalshi",
				Message: fmt.Sprintf("API returned status %d", resp.StatusCode),
				Err:     fmt.Errorf("status %d", resp.StatusCode),
			}
		}

		return resp, nil
	}

	return nil, lastErr
}

// retryBackoff returns the duration to wait before the next retry attempt.
// If the response included a Retry-After header with a positive integer number
// of seconds, that value is used directly. Otherwise exponential backoff is
// applied: retryBaseDelay × 2^attempt (500 ms, 1 s, 2 s for attempts 0–2).
func (c *KalshiClient) retryBackoff(attempt int, retryAfter string) time.Duration {
	if retryAfter != "" {
		if secs, err := strconv.Atoi(retryAfter); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return c.retryBaseDelay * (1 << attempt)
}

// signRequest attaches the three Kalshi authentication headers to req:
//
//	KALSHI-ACCESS-KEY       — the API key ID
//	KALSHI-ACCESS-TIMESTAMP — current time as milliseconds since epoch (string)
//	KALSHI-ACCESS-SIGNATURE — RSA-PSS SHA256 signature of (timestamp + METHOD + path)
//
// The path used for signing excludes query parameters, matching the Kalshi
// API specification.
func (c *KalshiClient) signRequest(req *http.Request) error {
	timestampMs := strconv.FormatInt(time.Now().UnixMilli(), 10)

	// Path only — query string is explicitly excluded from the signed message.
	path := req.URL.Path
	message := timestampMs + req.Method + path

	digest := sha256.Sum256([]byte(message))
	sig, err := rsa.SignPSS(rand.Reader, c.privateKey, crypto.SHA256, digest[:], &rsa.PSSOptions{
		SaltLength: rsa.PSSSaltLengthEqualsHash,
		Hash:       crypto.SHA256,
	})
	if err != nil {
		return &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "kalshi",
			Message: "failed to sign request",
			Err:     err,
		}
	}

	req.Header.Set("KALSHI-ACCESS-KEY", c.apiKeyID)
	req.Header.Set("KALSHI-ACCESS-TIMESTAMP", timestampMs)
	req.Header.Set("KALSHI-ACCESS-SIGNATURE", base64.StdEncoding.EncodeToString(sig))
	return nil
}

// truncate8 returns the first 8 characters of s, or all of s if shorter.
// Used to log partial credential values without exposing the full secret.
func truncate8(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}
