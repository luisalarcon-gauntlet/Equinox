# MASTER PROMPT — Project Equinox
# Read this entire document before writing a single line of code.
# This is the north star for everything you build.

---

## What You Are Building

Project Equinox is a cross-venue prediction market aggregation and routing
infrastructure prototype written in Go. It connects to two prediction market
venues (Kalshi and Polymarket), normalizes their market data into a single
canonical internal representation, detects equivalent markets across venues
using a hybrid heuristic + AI approach, and simulates intelligent routing
decisions for hypothetical trades.

This is NOT a trading product. No real money. No real orders. No wallets.
This is infrastructure research and a technical prototype.

---

## Who This Is For

This prototype is being presented to Peak6, a sophisticated trading and
private equity firm. They evaluate:
- How the problem is framed
- How the system is decomposed into layers
- How ambiguity is handled
- How decisions are justified
- Code readability, modularity, and documentation quality

UI polish does not matter. Architectural thinking does.

---

## Technology Decisions (All Final — Do Not Change)

- Language: Go 1.22
- Module: github.com/equinox
- Venues: Kalshi (public API) + Polymarket (public API)
- AI Layer: Anthropic Claude Sonnet (claude-sonnet-4-20250514)
- UI: Single HTML file embedded in Go binary via go:embed
- Server: Go standard library net/http (no external framework)
- Testing: Go standard library testing package (table-driven tests, TDD)
- Build: Makefile with make dev, make build, make test, make test-coverage

---

## Core Architecture — Four Layers

### Layer 1: Venue Connectors
- venues/kalshi/client.go — HTTP calls to Kalshi public API
- venues/polymarket/client.go — HTTP calls to Polymarket public API
- Both implement the VenueConnector interface in venues/connector.go
- Raw API responses stored in venue-specific structs (KalshiMarket, PolymarketMarket)
- Adapters transform raw structs into canonical Market struct

### Layer 2: Canonical Model (models/market.go)
The single internal representation every downstream component uses.
Contains: Identity, Pricing (bid/ask/mid), Liquidity, Timing, Metadata, RawData.
Routing engine and equivalence detector ONLY see this model. Never raw structs.

### Layer 3: Equivalence Detector (equivalence/)
Hybrid approach — heuristics first, AI fallback.

HEURISTIC LAYER (equivalence/heuristic.go):
- Normalize titles (lowercase, strip punctuation, collapse whitespace)
- Extract key entities (names, organizations, numbers, years)
- Compare resolution dates
- Score entity overlap
- If confidence >= 0.80 → match confirmed, done

AI TOOL LAYER (equivalence/tools/ + ai/client.go):
Only called when heuristic confidence < 0.80.
Five discrete tools Claude can use:
1. check_opposites — detects mirror image markets (GOP win = Dem loss)
2. check_synonyms — detects equivalent terminology (GOP = Republicans)
3. check_entity_match — scores named entity overlap
4. check_date_alignment — compares resolution dates with tolerance
5. check_structural_equivalence — compares question structure
Claude synthesizes tool results and returns: is_equivalent, are_opposites,
confidence (0-1), reasoning.

### Layer 4: Routing Engine (routing/engine.go)
Given a matched market pair, an order side (yes/no), and a hypothetical
order size ($), scores each venue and recommends the best one.

Scoring weights (justified design decisions):
- Price: 40% — directly affects P&L
- Liquidity: 35% — determines if order can fill
- Spread: 25% — market health indicator

Output: RoutingDecision with RecommendedVenue, VenueScores, Reasoning, Warnings.

---

## The Canonical Market Struct (Memorize This)

```go
type Market struct {
    ID         string      // internal UUID
    VenueID    string      // venue's native ID
    Venue      string      // "kalshi" or "polymarket"
    Title      string      // normalized question text
    YesBid     float64     // 0.0 to 1.0
    YesAsk     float64     // 0.0 to 1.0
    YesMid     float64     // (YesBid + YesAsk) / 2
    NoPrice    float64     // 1.0 - YesMid
    Spread     float64     // YesAsk - YesBid
    Liquidity  float64     // total $ in order book
    Volume24h  float64     // 24hr trading volume
    ResolvesAt time.Time   // market expiry
    FetchedAt  time.Time   // when we fetched this (staleness)
    Category   string      // "politics", "economics", "crypto", etc
    Status     string      // "open", "closed", "resolved"
    RawData    interface{} // original API response preserved
}
```

---

## Error Handling (Non-Negotiable)

Every error uses EquinoxError:
```go
type EquinoxError struct {
    Layer   string  // "connector", "normalizer", "equivalence", "ai", "routing", "server"
    Venue   string  // "kalshi", "polymarket", "anthropic", ""
    Message string
    Err     error
}
```

Error checkpoints required at:
- Every HTTP request (creation, execution, status check, body parse)
- Every JSON unmarshal
- Every date parse
- Every price validation
- Every Anthropic API call
- Every tool execution
- Every routing score calculation
- Every server handler

---

## Logging (Three Levels)

```go
logger.Info("layer", "venue", "message")   // normal operations
logger.Warn("layer", "venue", "message")   // degraded but continuing
logger.Error("layer", "venue", "message", err) // operation failed
```

Log format: [LEVEL][layer][venue] message (err if present)

---

## TDD Approach

Write tests BEFORE implementation. Every component has tests first.
Use table-driven tests throughout.
Mock all external dependencies via interfaces.
See testing.mdc for complete test case list.

---

## HTTP API Endpoints

- GET  /search?q={query} → []MatchResult
- POST /route → RoutingDecision
- GET  /health → {"status":"ok"}
- GET  / → embedded index.html

---

## Web UI

Single page app in static/index.html embedded in binary.
Shows: search input, matched market pairs side by side with scores,
routing decision panel with reasoning and warnings.
Calls /search and /route endpoints via fetch().
No external CSS frameworks required — plain HTML/CSS is fine.

---

## Environment Variables

```
ANTHROPIC_API_KEY=your_key_here    # required
KALSHI_BASE_URL=...                # optional, has default
POLYMARKET_BASE_URL=...            # optional, has default
SERVER_PORT=8080                   # optional, default 8080
HTTP_TIMEOUT=10s                   # optional, default 10s
```

---

## What Success Looks Like

1. Run: make dev
2. Open: localhost:8080
3. Type: "2026 midterm elections"
4. See: Matched markets from Kalshi and Polymarket side by side
5. Click: "Route $500 Buy Yes"
6. See: "Route to Kalshi — better price, tighter spread, 5x liquidity"
7. See: Any warnings (stale data, mixed signals, etc)

Run: make test
See: All tests passing with 90%+ coverage

---

## Presentation Talking Points (For Peak6 Meeting)

1. "Built in Go to align with Peak6's internal stack. Concurrency model fits
   parallel API requests. Strong typing enforces canonical model at compile time."

2. "Hybrid equivalence detection — heuristics first, AI as deliberate fallback.
   Every decision is logged with its path. AI usage is auditable and cost-efficient."

3. "The check_opposites tool handles the hardest matching case — markets that are
   mirror images of the same event, like GOP winning vs Democrats winning."

4. "Routing engine scores three weighted metrics with documented justification.
   Weights are configurable. Every decision produces a human-readable explanation."

5. "Full test suite written TDD. Table-driven tests throughout. Coverage reports
   available with make test-coverage."

6. "Single binary deployment. UI embedded at compile time. One command to run.
   Cross-platform by default."
