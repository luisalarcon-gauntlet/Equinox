# Architecture — Project Equinox

## System Overview

Project Equinox is a cross-venue prediction market aggregation and routing
infrastructure prototype written in Go 1.22. It connects to two prediction
market venues (Kalshi and Polymarket), normalises their market data into a
single canonical internal representation, detects equivalent markets across
venues using a hybrid heuristic + AI approach, and simulates intelligent
routing decisions for hypothetical trades.

This is **not** a trading product. No real money, no real orders, no wallets.
This is infrastructure research and a technical prototype.

---

## Layer Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│  HTTP Client (browser / curl)                                   │
└──────────────────────┬──────────────────────────────────────────┘
                       │  GET /search  POST /route  GET /health
                       ▼
┌─────────────────────────────────────────────────────────────────┐
│  Layer 7: HTTP Server  (server/server.go)                       │
│  • Thin handlers — no business logic                            │
│  • Parallel venue fetches via goroutines                        │
│  • Match cache keyed by market ID                               │
│  • Serves embedded index.html                                   │
└───────────┬──────────────────────────────────┬──────────────────┘
            │                                  │
            ▼                                  ▼
┌───────────────────────┐          ┌───────────────────────────────┐
│ Layer 1: Connectors   │          │ Layer 6: Routing Engine       │
│  venues/kalshi/       │          │  routing/engine.go            │
│  venues/polymarket/   │          │  • Min-max score normalisation│
│                       │          │  • Price 40% / Liq 35% /      │
│  client.go  ←HTTP→   │          │    Spread 25%                 │
│  adapter.go           │          │  • Staleness detection        │
│  models.go            │          │  • Mixed-signal warnings       │
└──────────┬────────────┘          └───────────────────────────────┘
           │  []models.Market
           ▼
┌─────────────────────────────────────────────────────────────────┐
│  Layer 2: Canonical Model  (models/market.go)                   │
│  • Single struct every layer works with — never bypassed        │
│  • Market  •  MatchResult  •  VenueScore  •  RoutingDecision    │
└───────────────────────────┬─────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│  Layer 3: Equivalence Detector  (equivalence/)                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │ Heuristic Matcher  (heuristic.go)                        │   │
│  │  • Title normalisation                                   │   │
│  │  • Entity extraction (names, numbers, dates, orgs)       │   │
│  │  • Date proximity scoring                                │   │
│  │  • Confidence ≥ 0.80 → return (Method: "heuristic")     │   │
│  └───────────────────────┬──────────────────────────────────┘   │
│                          │ confidence < 0.80                     │
│                          ▼                                       │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │ AI Tool Layer  (equivalence/tools/ + ai/client.go)       │   │
│  │  • check_synonyms   — GOP = Republicans                  │   │
│  │  • check_entity_match — named entity overlap             │   │
│  │  • check_date_alignment — resolution date proximity      │   │
│  │  • check_structural — question structure comparison      │   │
│  │  Tools run in parallel → compact signals sent to OpenAI  │   │
│  │  OpenAI returns: is_match, confidence, reasoning         │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

---

## Key Design Decisions

### 1. Canonical Model as the Contract

Every layer boundary is crossed using `models.Market`. Adapters at the venue
layer transform raw API responses into this struct before any data leaves the
`venues/` package. The routing engine, equivalence detector, and HTTP server
never see `KalshiMarket` or `PolymarketMarket` — compile-time enforcement of
the contract.

**Why:** Prevents venue-specific logic from leaking into business components.
Adding a third venue requires only a new adapter; no other layer changes.

### 2. Heuristics-First, AI as Deliberate Fallback

The heuristic layer runs first on every pair. AI is invoked only when
heuristic confidence falls below 0.80. This decision is logged and auditable.

**Why:**
- Cost: ~95% of pairs can be decided without an API call.
- Latency: heuristic matching is microseconds; `gpt-4.1-nano` is a low-latency fallback.
- Auditability: every decision has a logged reason (`Method` field).
- Resilience: AI unavailability degrades gracefully to heuristic-only.

### 3. Parallel Execution at Two Points

1. `/search` fetches both venues concurrently using goroutines.
2. Within the AI layer, all five tools execute in parallel using goroutines.

**Why:** Both venues are independent; all five tools are independent. Wall
time is bounded by the slowest response rather than the sum.

### 4. Typed Error Wrapping

All errors use `EquinoxError` with a `Layer` and `Venue` field:

```go
type EquinoxError struct {
    Layer   string
    Venue   string
    Message string
    Err     error
}
```

**Why:** Log analysis and error attribution require knowing *where* in the
pipeline a failure occurred. Raw errors lose this context.

### 5. Single Binary, Embedded UI

The HTML UI is embedded in the binary at compile time via `//go:embed`. The
binary needs no external files at runtime.

**Why:** One-command deployment. No static file server, no CDN, no file path
configuration. Works identically on every OS.

### 6. No Global State

All components receive their dependencies via constructors. No `init()`
functions, no package-level variables that mutate at runtime.

**Why:** Predictable test isolation. No test-order dependencies.

---

## Technology Choices and Justifications

| Choice | Rationale |
|--------|-----------|
| **Go 1.22** | Aligns with Peak6's internal stack. Strong typing enforces canonical model at compile time. Goroutine model fits parallel API requests. |
| **Standard library HTTP** | No external framework dependencies. `net/http` is production-grade and well-understood. |
| **OpenAI GPT-4.1 nano** | Cheap, high-volume classification for ambiguous market pairs. |
| **UUID v5 for market IDs** | Deterministic IDs from venue ticker strings. The same market always gets the same internal ID across fetches, enabling cache deduplication. |
| **`godotenv`** | Developer convenience only — production deployments set env vars directly. |
| **`google/uuid`** | Minimal, well-maintained UUID library. Single transitive dependency. |

---

## Known Limitations and Tradeoffs

| Limitation | Reason |
|------------|--------|
| **No persistent cache** | The match cache lives in memory and resets on each `/search` call. A production system would use Redis or a database. |
| **O(n²) pair comparison** | The equivalence detector evaluates every kalshi×polymarket pair. Acceptable for O(10–50) results per query but would require pre-filtering (embedding similarity, BM25) at scale. |
| **No authentication** | Kalshi's public API endpoints require no auth for read access. A production system would use authenticated endpoints for real-time CLOB data. |
| **Single process** | All components run in one process. A production system would separate the venue fetchers (high I/O) from the equivalence detector (high CPU) and the routing engine. |
| **No rate limiting** | The `/search` endpoint makes multiple outbound HTTP calls per request. Production would require circuit breakers and rate limiters. |

---

## How to Add a New Venue

Adding a third venue (e.g. Manifold Markets) requires exactly four steps:

1. **Create `venues/manifold/models.go`** — mirror the raw API response struct.
2. **Create `venues/manifold/adapter.go`** — implement `AdaptManifoldMarket(raw ManifoldMarket) (models.Market, error)`.
3. **Create `venues/manifold/client.go`** — implement `VenueConnector` interface (two methods: `FetchMarkets`, `GetVenueName`).
4. **Register in `main.go`** — add `manifoldClient` to the `connectors` slice.

No other layer requires changes. The routing engine, equivalence detector,
server, and UI are all venue-agnostic by design.

The equivalence detector currently assumes a two-venue world (Kalshi vs
Polymarket). Extending to N venues requires upgrading from O(n²) pair
evaluation to a smarter pre-filtering approach before calling the detector.

---

## Configuration

All configuration is loaded from environment variables at startup. See
`.env.example` for the full list. Sensible defaults are applied for all
optional fields; only `OPENAI_API_KEY` is required.

```go
type Config struct {
    OpenAIAPIKey                 string        // required
    OpenAIBaseURL                string        // default: OpenAI API
    KalshiBaseURL                string        // default: Kalshi production API
    PolymarketBaseURL            string        // default: Polymarket Gamma API
    HTTPTimeout                  time.Duration // default: 10s
    ServerPort                   string        // default: 8080
    HeuristicConfidenceThreshold float64       // default: 0.80
    PriceDataStalenessThreshold  time.Duration // default: 2m
}
```

---

## HTTP API

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Returns `{"status":"ok"}`. Used by the UI health indicator. |
| `GET` | `/search?q={query}` | Fetches markets from both venues, runs equivalence detection, returns `[]MatchResult`. |
| `POST` | `/route` | Accepts `{market_id, side, size}`. Looks up cached match and returns `RoutingDecision`. |
| `GET` | `/` | Serves the embedded `index.html`. |

The match cache is populated by `/search` and consumed by `/route`. Calling
`/route` without a prior `/search` that returned the requested market ID
returns 404. This is intentional: the routing engine requires both canonical
markets to be available to score them against each other.
