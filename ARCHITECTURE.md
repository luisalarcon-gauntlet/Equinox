# Architecture — Project Equinox

## Overview

Project Equinox is a cross-venue prediction market aggregation and routing
infrastructure prototype. It connects to Kalshi and Polymarket, normalizes
their market data into a single canonical representation, detects equivalent
markets across venues, and simulates intelligent routing decisions for
hypothetical trades.

This is an infrastructure prototype, not a trading product.

---

## System Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                        HTTP Server                          │
│                    GET /search  POST /route                 │
└───────────────────────────┬─────────────────────────────────┘
                            │
            ┌───────────────┴───────────────┐
            │                               │
┌───────────▼──────────┐       ┌────────────▼────────────┐
│  Equivalence Detector │       │     Routing Engine      │
│  (heuristic + AI)     │       │  (price/spread/liquidity│
└───────────┬──────────┘       └────────────┬────────────┘
            │                               │
            └───────────────┬───────────────┘
                            │
                   ┌────────▼────────┐
                   │ Canonical Market │
                   │     Models       │
                   └────────┬────────┘
                            │
            ┌───────────────┴───────────────┐
            │                               │
┌───────────▼──────────┐       ┌────────────▼────────────┐
│   Kalshi Connector    │       │  Polymarket Connector   │
│  client + adapter     │       │  client + adapter       │
└───────────┬──────────┘       └────────────┬────────────┘
            │                               │
    Kalshi Public API             Polymarket Public API
```

---

## Layer Separation — The Core Principle

The system is decomposed into four strictly separated layers.
No layer has knowledge of layers below or beside it. Each layer
only speaks in the canonical Market model.

### Layer 1: Venue Connectors
Responsibility: Talk to external APIs. Transform raw responses into
canonical Market structs. Know nothing about equivalence or routing.

Files:
- venues/connector.go — VenueConnector interface
- venues/kalshi/client.go — HTTP calls to Kalshi
- venues/kalshi/models.go — raw Kalshi API structs
- venues/kalshi/adapter.go — transforms KalshiMarket → Market
- venues/polymarket/client.go — HTTP calls to Polymarket
- venues/polymarket/models.go — raw Polymarket API structs
- venues/polymarket/adapter.go — transforms PolymarketMarket → Market

### Layer 2: Canonical Models
Responsibility: Define the single internal language the entire system speaks.

Files:
- models/market.go — Market, MatchResult, VenueScore, RoutingDecision

### Layer 3: Equivalence Detector
Responsibility: Determine which markets across venues refer to the same
real-world event. Know nothing about routing.

Files:
- equivalence/detector.go — orchestrates hybrid detection
- equivalence/heuristic.go — rule-based matching
- equivalence/tools/ — five discrete AI tools

### Layer 4: Routing Engine
Responsibility: Given equivalent markets, decide which venue is better
for a hypothetical order. Know nothing about venue-specific details.
Only sees canonical Market structs.

Files:
- routing/engine.go — scoring, decision, reasoning

---

## Key Design Decisions

### Decision 1: Go as the Implementation Language
Go was chosen to align with Peak6's internal stack. Additionally, Go's
concurrency model (goroutines, channels) is well-suited for parallel
API requests to multiple venues. Strong static typing enforces the
canonical market model at compile time — you cannot accidentally pass
a KalshiMarket where a canonical Market is expected.

### Decision 2: Adapter Pattern for Venue Normalization
Each venue has its own raw struct that mirrors the API response exactly,
and a separate adapter that transforms it into the canonical form. This
means venue-specific parsing logic is completely isolated. Adding a third
venue requires only a new connector and adapter — nothing else changes.

### Decision 3: Hybrid Equivalence Detection
Pure rule-based matching is brittle — it fails on semantic equivalence
("GOP" vs "Republicans"). Pure AI matching is a black box with latency
and cost. The hybrid approach runs deterministic heuristics first (fast,
free, explainable) and escalates to AI only when confidence is below
threshold (0.80). Every decision is logged with its path.

### Decision 4: Tool-Based AI Layer
Rather than asking Claude one vague question, the AI layer provides
Claude with five discrete tools (check_opposites, check_synonyms, etc).
Claude decides which tools to run, runs them, and synthesizes the results.
This makes the AI decision transparent and auditable — every tool call
is logged with its result and reasoning.

### Decision 5: Weighted Routing Scores
The routing engine scores three metrics with documented weights:
price (40%), liquidity (35%), spread (25%). Weights are explicit
constants with comments justifying each value. This is intentionally
transparent — the reasoning behind every routing decision can be
explained and the weights can be adjusted.

### Decision 6: Single Binary Deployment
The HTML UI is embedded in the Go binary at compile time using go:embed.
The entire application — logic, web server, UI — compiles to one
self-contained executable. No runtime dependencies. No file management.
Cross-platform by default. One command to run.

---

## How to Add a New Venue

Adding a third venue (e.g. Manifold Markets) requires exactly these steps:
1. Create venues/manifold/models.go — raw API struct
2. Create venues/manifold/adapter.go — transform to canonical Market
3. Create venues/manifold/client.go — implement VenueConnector interface
4. Register the new connector in main.go

The equivalence detector, routing engine, and server require zero changes.
This demonstrates the value of the canonical model and interface design.

---

## Known Limitations

1. No real-time price streaming — prices are fetched on demand, not pushed.
   In production, a websocket or polling layer would be needed.

2. Equivalence detection confidence thresholds are heuristic — the 0.80
   cutoff is a reasonable default but would need tuning with real data.

3. Routing weights are assumptions — the 40/35/25 weighting has not been
   validated against real execution data. In production, these would be
   backtested and adjusted.

4. No order book depth analysis — liquidity is treated as a single number.
   Real routing would analyze depth at specific price levels.

5. Public API rate limits — no rate limiting or backoff is implemented
   beyond basic timeouts. Production would need proper rate limit handling.

---

## Technology Stack Summary

| Component       | Technology              | Justification                          |
|----------------|-------------------------|----------------------------------------|
| Language        | Go 1.22                 | Peak6 stack, concurrency, typed        |
| HTTP Server     | net/http (stdlib)       | No external framework needed           |
| UI Embedding    | embed (stdlib)          | Single binary deployment               |
| UUID Generation | github.com/google/uuid  | Standard, well-tested                  |
| Env Loading     | github.com/joho/godotenv| Standard Go env pattern                |
| AI Layer        | OpenAI GPT-4.1 nano     | Cheap, high-volume boolean classification |
| Testing         | testing (stdlib)        | Go standard, table-driven              |
| Build           | Makefile                | Simple, universal, no build tool needed|
