package kalshi_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/equinox/config"
	"github.com/equinox/logger"
	"github.com/equinox/venues/kalshi"
)

// generateTestKeyFile creates a temporary RSA private key PEM file for use in
// tests. The file is removed automatically via t.Cleanup. Returns the key ID
// (fixed "test-key-id") and the absolute path to the PEM file.
func generateTestKeyFile(t *testing.T) (keyID, keyPath string) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generateTestKeyFile: rsa.GenerateKey: %v", err)
	}

	keyBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("generateTestKeyFile: MarshalPKCS8PrivateKey: %v", err)
	}

	tmpFile, err := os.CreateTemp("", "equinox-test-kalshi-key-*.pem")
	if err != nil {
		t.Fatalf("generateTestKeyFile: CreateTemp: %v", err)
	}
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })

	if err := pem.Encode(tmpFile, &pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes}); err != nil {
		t.Fatalf("generateTestKeyFile: pem.Encode: %v", err)
	}
	tmpFile.Close()

	return "test-key-id", tmpFile.Name()
}

// newTestClient creates a KalshiClient wired to the provided mock server URL,
// using a freshly generated RSA key so signing works without real credentials.
//
// Two test-only overrides are applied so tests run fast:
//   - retryBaseDelay → 0  (429 retries are instant, no sleeping)
//   - rateLimiter    → unlimited  (no token-bucket delay between requests)
func newTestClient(t *testing.T, serverURL string) *kalshi.KalshiClient {
	t.Helper()

	keyID, keyPath := generateTestKeyFile(t)
	cfg := &config.Config{
		KalshiBaseURL:    serverURL,
		KalshiAPIKeyID:   keyID,
		KalshiAPIKeyPath: keyPath,
		HTTPTimeout:      5 * time.Second,
	}
	log := logger.New(io.Discard)
	client, err := kalshi.NewKalshiClient(cfg, log)
	if err != nil {
		t.Fatalf("NewKalshiClient: %v", err)
	}
	kalshi.SetRetryBaseDelay(client, 0)
	kalshi.SetRateLimiter(client, rate.NewLimiter(rate.Inf, 1))
	return client
}

// oneEventResponse returns a KalshiEventsResponse containing a single event
// with one nested market leg — used for happy-path client tests.
func oneEventResponse() kalshi.KalshiEventsResponse {
	return kalshi.KalshiEventsResponse{
		Events: []kalshi.KalshiEvent{
			{
				EventTicker: "KXTEST",
				Title:       "Test Market Event",
				Category:    "other",
				Markets: []kalshi.KalshiMarket{
					{
						Ticker:         "KXTEST-26NOV01-Y",
						EventTicker:    "KXTEST",
						YesSubTitle:    "Will the test market resolve Yes?",
						YesBidDollars:  "0.44",
						YesAskDollars:  "0.46",
						CloseTime:      "2026-11-01T00:00:00Z",
						Status:         "active",
						OpenInterestFp: "5000.00",
						Volume24h:      1200,
					},
				},
			},
		},
		Cursor: "",
	}
}

// ---------------------------------------------------------------------------
// TestKalshiFetchReturnsMarkets — happy path
// ---------------------------------------------------------------------------

func TestKalshiFetchReturnsMarkets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(oneEventResponse()); err != nil {
			t.Errorf("mock server encode error: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	markets, err := client.FetchMarkets(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(markets) != 1 {
		t.Fatalf("got %d markets, want 1", len(markets))
	}

	m := markets[0]
	if m.Venue != "kalshi" {
		t.Errorf("Venue = %q, want %q", m.Venue, "kalshi")
	}
	if m.VenueID != "KXTEST-26NOV01-Y" {
		t.Errorf("VenueID = %q, want %q", m.VenueID, "KXTEST-26NOV01-Y")
	}
	if m.YesBid != 0.44 {
		t.Errorf("YesBid = %v, want 0.44", m.YesBid)
	}
}

func TestKalshiFetchSetsAcceptHeader(t *testing.T) {
	var capturedHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeader = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(oneEventResponse()); err != nil {
			t.Errorf("mock server encode error: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.FetchMarkets(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedHeader != "application/json" {
		t.Errorf("Accept header = %q, want %q", capturedHeader, "application/json")
	}
}

func TestKalshiFetchWithQuerySendsURLParams(t *testing.T) {
	var capturedURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(kalshi.KalshiEventsResponse{}); err != nil {
			t.Errorf("mock server encode error: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	// Query filtering is client-side; the URL must always carry limit/status/with_nested_markets.
	_, err := client.FetchMarkets(context.Background(), "bitcoin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedURL == "" {
		t.Error("expected query parameters in request URL, got empty")
	}
}

// ---------------------------------------------------------------------------
// TestKalshiFetchSetsAuthHeaders — every request must carry all three
// Kalshi authentication headers with non-empty values.
// ---------------------------------------------------------------------------

func TestKalshiFetchSetsAuthHeaders(t *testing.T) {
	var (
		accessKey       string
		accessTimestamp string
		accessSignature string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accessKey = r.Header.Get("KALSHI-ACCESS-KEY")
		accessTimestamp = r.Header.Get("KALSHI-ACCESS-TIMESTAMP")
		accessSignature = r.Header.Get("KALSHI-ACCESS-SIGNATURE")
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(kalshi.KalshiEventsResponse{}); err != nil {
			t.Errorf("mock server encode error: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.FetchMarkets(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if accessKey != "test-key-id" {
		t.Errorf("KALSHI-ACCESS-KEY = %q, want %q", accessKey, "test-key-id")
	}
	if accessTimestamp == "" {
		t.Error("KALSHI-ACCESS-TIMESTAMP is empty, want a millisecond timestamp")
	}
	if accessSignature == "" {
		t.Error("KALSHI-ACCESS-SIGNATURE is empty, want a base64-encoded RSA-PSS signature")
	}
}

// ---------------------------------------------------------------------------
// TestKalshiFetchHandlesTimeout — context deadline exceeded
// ---------------------------------------------------------------------------

func TestKalshiFetchHandlesTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until the client's context expires.
		select {
		case <-r.Context().Done():
			return
		case <-time.After(2 * time.Second):
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	_, err := client.FetchMarkets(ctx, "")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

// ---------------------------------------------------------------------------
// TestKalshiFetchHandlesNon200 — API returns error status
// ---------------------------------------------------------------------------

func TestKalshiFetchHandlesNon200(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{"429 Too Many Requests", http.StatusTooManyRequests},
		{"500 Internal Server Error", http.StatusInternalServerError},
		{"401 Unauthorized", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
			}))
			defer server.Close()

			client := newTestClient(t, server.URL)
			_, err := client.FetchMarkets(context.Background(), "")
			if err == nil {
				t.Fatalf("expected error for status %d, got nil", tt.status)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestKalshiFetchHandlesMalformedJSON — response body is not valid JSON
// ---------------------------------------------------------------------------

func TestKalshiFetchHandlesMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{this is not: valid json[[[`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.FetchMarkets(context.Background(), "")
	if err == nil {
		t.Fatal("expected JSON decode error, got nil")
	}
}

// ---------------------------------------------------------------------------
// TestKalshiFetchHandlesEmptyResponse — API returns zero markets
// ---------------------------------------------------------------------------

func TestKalshiFetchHandlesEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		empty := kalshi.KalshiEventsResponse{
			Events: []kalshi.KalshiEvent{},
			Cursor: "",
		}
		if err := json.NewEncoder(w).Encode(empty); err != nil {
			t.Errorf("mock server encode error: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	markets, err := client.FetchMarkets(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(markets) != 0 {
		t.Errorf("got %d markets, want 0", len(markets))
	}
}

// ---------------------------------------------------------------------------
// TestKalshiGetVenueName
// ---------------------------------------------------------------------------

func TestKalshiGetVenueName(t *testing.T) {
	client := newTestClient(t, "http://localhost")
	if got := client.GetVenueName(); got != "kalshi" {
		t.Errorf("GetVenueName() = %q, want %q", got, "kalshi")
	}
}

// ---------------------------------------------------------------------------
// TestKalshiFetchSkipsInvalidMarkets — adapter errors are collected, not fatal
// ---------------------------------------------------------------------------

func TestKalshiFetchSkipsInvalidMarkets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// One event with two legs: one valid, one with empty ticker (adapter error).
		resp := kalshi.KalshiEventsResponse{
			Events: []kalshi.KalshiEvent{
				{
					EventTicker: "KXGOOD",
					Title:       "Good Test Event",
					Category:    "other",
					Markets: []kalshi.KalshiMarket{
						{
							Ticker:        "KXGOOD-26NOV01-Y",
							EventTicker:   "KXGOOD",
							YesBidDollars: "0.50",
							YesAskDollars: "0.52",
							CloseTime:     "2026-11-01T00:00:00Z",
							Status:        "active",
							OpenInterest:  100,
							Volume24h:     50,
						},
						{
							// Invalid: empty ticker → adapter returns error, leg is skipped.
							Ticker:        "",
							YesBidDollars: "0.50",
							YesAskDollars: "0.52",
							CloseTime:     "2026-11-01T00:00:00Z",
						},
					},
				},
			},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("mock server encode error: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	markets, err := client.FetchMarkets(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only the valid market should be returned; the invalid one is skipped.
	if len(markets) != 1 {
		t.Errorf("got %d markets, want 1 (invalid market should be skipped)", len(markets))
	}
}

// ---------------------------------------------------------------------------
// TestNewKalshiClientRejectsMissingKeyFile — constructor fails gracefully
// ---------------------------------------------------------------------------

func TestNewKalshiClientRejectsMissingKeyFile(t *testing.T) {
	cfg := &config.Config{
		KalshiBaseURL:    "http://localhost",
		KalshiAPIKeyID:   "some-id",
		KalshiAPIKeyPath: "/nonexistent/path/to/key.pem",
		HTTPTimeout:      5 * time.Second,
	}
	log := logger.New(io.Discard)
	_, err := kalshi.NewKalshiClient(cfg, log)
	if err == nil {
		t.Fatal("expected error when key file does not exist, got nil")
	}
}

// ---------------------------------------------------------------------------
// 429 retry behaviour
// ---------------------------------------------------------------------------

// TestKalshiRetryOn429ThenSuccess verifies that doSignedGet retries on 429
// and succeeds once the server starts returning 200. The mock server returns
// 429 on the first two attempts and 200 on the third. FetchMarkets must
// return one market and the server must have been hit exactly three times.
func TestKalshiRetryOn429ThenSuccess(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(oneEventResponse()); err != nil {
			t.Errorf("mock server encode error: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	markets, err := client.FetchMarkets(context.Background(), "")
	if err != nil {
		t.Fatalf("expected success after retries, got error: %v", err)
	}
	if len(markets) != 1 {
		t.Errorf("want 1 market, got %d", len(markets))
	}
	if got := int(attempts.Load()); got != 3 {
		t.Errorf("want 3 server hits (2×429 + 1×200), got %d", got)
	}
}

// TestKalshiRetryOn429ExhaustsRetries verifies that doSignedGet gives up
// after maxRetries and returns an error when the server always returns 429.
// The server must be hit exactly maxRetries+1 times (1 initial + 3 retries).
func TestKalshiRetryOn429ExhaustsRetries(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.FetchMarkets(context.Background(), "")
	if err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}
	// 1 initial attempt + 3 retries = 4 total server hits.
	if got := int(attempts.Load()); got != 4 {
		t.Errorf("want 4 server hits (1 initial + 3 retries), got %d", got)
	}
}
