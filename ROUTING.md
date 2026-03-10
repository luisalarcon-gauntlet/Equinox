# Routing Engine — Project Equinox

## The Problem

Once equivalent markets are identified across venues, the question becomes:
for a hypothetical order, which venue should it be sent to and why?

This is the routing problem. The routing engine evaluates available venues
for a hypothetical order and produces a decision with a clear explanation.

The goal is not to optimize execution quality with mathematical precision.
The goal is to make a defensible, explainable decision based on observable
market quality metrics.

---

## Inputs

The routing engine receives:
1. A MatchResult — the equivalent market pair from equivalence detection
2. OrderSide — "yes" or "no"
3. OrderSize — hypothetical dollar amount (e.g. $500)

---

## Metrics

Three metrics are evaluated per venue. All are derived from the canonical
Market struct — the routing engine has zero knowledge of venue-specific details.

### Metric 1: Price (Weight: 40%)
The price you pay directly determines your P&L. A better price at entry
is the most important factor.

For a "buy yes" order:
- Lower YesAsk = better (you pay less)
- Score = 1 - (venue_ask / max_ask_across_venues)
- Higher score = better price

For a "buy no" order:
- NoPrice = 1 - YesMid
- Lower NoPrice = better
- Score calculated symmetrically

Price is weighted highest (40%) because it has direct, immediate impact
on the value of the position.

### Metric 2: Liquidity (Weight: 35%)
Liquidity determines whether the order can be filled at the quoted price.
A thin order book means your order moves the market against you.

- Higher Liquidity = better
- Score = venue_liquidity / max_liquidity_across_venues
- Higher score = deeper book

Liquidity is weighted second (35%) because a great price means nothing
if the order cannot be filled at that price.

### Metric 3: Spread (Weight: 25%)
The spread (YesAsk - YesBid) is a proxy for market health and transaction
cost. A tight spread indicates an active, competitive market.

- Lower Spread = better
- Score = 1 - (venue_spread / max_spread_across_venues)
- Higher score = tighter spread

Spread is weighted third (25%) because it is partially captured by the
price metric and serves primarily as a confirmation signal.

---

## Scoring Algorithm

```
For each venue:
    PriceScore     = normalize price across venues (higher = better)
    LiquidityScore = normalize liquidity across venues (higher = better)
    SpreadScore    = normalize spread across venues (lower spread = higher score)

    TotalScore = (PriceScore    * 0.40)
               + (LiquidityScore * 0.35)
               + (SpreadScore    * 0.25)

RecommendedVenue = venue with highest TotalScore
```

All scores are normalized to 0.0–1.0. All weights sum to 1.0.

---

## Routing Decision Output

```go
type RoutingDecision struct {
    Market           Market      // the market being routed
    OrderSide        string      // "yes" or "no"
    OrderSize        float64     // hypothetical $ amount
    RecommendedVenue string      // "kalshi" or "polymarket"
    Confidence       float64     // how clear-cut the decision was
    VenueScores      []VenueScore // full breakdown per venue
    Reasoning        string      // human-readable explanation
    Warnings         []string    // caveats and concerns
    DecidedAt        time.Time
}
```

---

## Example Decision

**Query:** Route $500 Buy Yes on "Democrats control House 2026"

```
VENUE SCORING
─────────────────────────────────────────────────────
           KALSHI          POLYMARKET
Price      $0.63 → 0.92    $0.67 → 0.71
Spread     $0.02 → 0.95    $0.08 → 0.61
Liquidity  $250k → 0.88    $45k  → 0.54
─────────────────────────────────────────────────────
Total      0.92            0.63
─────────────────────────────────────────────────────

DECISION: Route to KALSHI
Confidence: 0.89

REASONING: Kalshi offers a 4 cent price advantage on the Yes ask
($0.63 vs $0.67), a significantly tighter spread ($0.02 vs $0.08),
and approximately 5x deeper liquidity ($250k vs $45k). At a $500
order size, price impact on Polymarket would be material given its
shallow book. Kalshi is the clear choice across all three metrics.

WARNINGS: None
```

---

## Edge Cases

### Mixed Signals
When one venue has a better price but the other has better liquidity:

```
DECISION: Route to KALSHI (confidence: 0.61)
REASONING: Kalshi offers a better price ($0.63 vs $0.61), but Polymarket
has 3x deeper liquidity. At the requested order size of $500, liquidity
risk on Kalshi is low. Price advantage is the tiebreaker.
WARNING: Mixed signals — Polymarket liquidity advantage partially offsets
Kalshi price advantage. Recommend monitoring fill quality.
```

### Tie (Scores Within 5%)
When venue scores are too close to call:

```
DECISION: Route to KALSHI (confidence: 0.52)
REASONING: Venue scores are within 5% margin (Kalshi: 0.74, Polymarket: 0.71).
No strong routing preference detected. Defaulting to higher liquidity venue
(Kalshi: $250k vs Polymarket: $230k).
WARNING: Decision confidence is low — scores are nearly equal. Either
venue is a reasonable choice.
```

### One Venue Unavailable
When one venue's API is down or returned an error:

```
DECISION: Route to POLYMARKET (confidence: 1.00)
REASONING: Kalshi data unavailable at time of routing decision.
Routing to Polymarket by default.
WARNING: Kalshi was unavailable — this decision is not a comparative
routing. Recommend retrying when Kalshi data is available.
```

### Stale Data
When price data is older than the staleness threshold (2 minutes):

```
DECISION: Route to KALSHI (confidence: 0.81)
REASONING: Kalshi offers better price and liquidity on available data.
WARNING: Kalshi price data is 4 minutes old (fetched at 14:32:11).
Routing decision may not reflect current market conditions. Recommend
refreshing data before executing.
```

### Order Size Too Large for Liquidity
When the hypothetical order size exceeds available liquidity:

```
DECISION: Route to KALSHI (confidence: 0.88)
REASONING: Kalshi has superior metrics across all three criteria.
WARNING: Requested order size ($5,000) exceeds 10% of Kalshi's available
liquidity ($45,000). Significant price impact expected. Consider splitting
the order or reducing size.
```

---

## What Was Not Implemented

### Order Book Depth Analysis
The routing engine treats liquidity as a single number. In reality,
the order book has levels — $10k at $0.63, another $50k at $0.64, etc.
True routing would analyze the full depth to estimate price impact at
the exact order size. This was out of scope for the prototype but is
the most important production enhancement.

### Historical Fill Quality
The best routing systems incorporate historical data — which venue
actually fills orders at quoted prices, which has hidden slippage,
which has faster execution. No historical data is available in this
prototype.

### Dynamic Weight Adjustment
The 40/35/25 weights are static. A production system would backtest
these weights against historical data and potentially adjust them
dynamically based on market conditions (e.g., in low-liquidity
environments, liquidity weight should increase).

### Cross-Venue Arbitrage Detection
If the same market trades at $0.63 on Kalshi and $0.67 on Polymarket,
that is a potential arbitrage. The routing engine currently selects
the better venue but does not flag or quantify the arbitrage opportunity.
This would be a natural extension of the current scoring system.
