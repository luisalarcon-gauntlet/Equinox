# Routing Engine — docs/ROUTING.md

## Problem Definition

Given two equivalent prediction markets — one from Kalshi, one from Polymarket —
and a hypothetical order (side: yes/no, size: $amount), the routing engine must
recommend the venue that maximises expected value for the trader.

This is not order execution. No real orders are placed. The engine produces a
`RoutingDecision` that explains which venue is preferable and why, along with
any caveats the trader should know about.

---

## Metrics and Why We Use Them

Three metrics capture the economically meaningful dimensions of market quality
for a prediction market participant.

### 1. Price (weight: 40%)

**What it measures:** The effective cost of acquiring a unit position.

- For a **yes** buyer: `YesAsk` — the lowest price someone will sell yes.
- For a **no** buyer: `NoPrice` = `1 − YesMid` — the implied cost of a no
  position derived from the midpoint.

**Why it is highest-weighted:** Price directly determines P&L at entry. A 1¢
difference in entry price on a $500 order is $5 off the top — before any
movement in the market's probability.

### 2. Liquidity (weight: 35%)

**What it measures:** Total dollar value available in the order book
(`Liquidity` field from the canonical Market struct).

**Why it is second:** A great price is worthless if the order cannot fill.
Thin books mean slippage; our hypothetical order size determines how much of
the book gets consumed. Deeper liquidity means the stated price is more likely
to be achievable.

### 3. Spread (weight: 25%)

**What it measures:** `YesAsk − YesBid` — the bid-ask spread.

**Why it is third:** Spread is a health indicator, not a direct cost (since we
route at ask or mid, not half-spread). A tight spread signals active
market-making, competitive pricing, and low adverse-selection risk. Wide
spreads often precede thin liquidity and stale prices.

### Weight Invariant

```
PriceWeight + LiquidityWeight + SpreadWeight = 0.40 + 0.35 + 0.25 = 1.00
```

---

## Scoring Algorithm

### Cross-Venue Normalisation (Route)

When both venues are available the engine uses **min-max normalisation** across
the two venues for each metric. This produces scores in [0, 1] where **higher
is always better**, regardless of the raw units.

```
For a metric where higher raw value is better (Liquidity):
  score_A = (val_A − min) / (max − min)
  score_B = (val_B − min) / (max − min)

For a metric where lower raw value is better (Price, Spread):
  score_A = (max − val_A) / (max − min)   ← inverted
  score_B = (max − val_B) / (max − min)

Tie on a metric (val_A == val_B): both receive 0.5
```

Each venue's `TotalScore` is the weighted sum of its three sub-scores:

```
TotalScore = PriceScore × 0.40 + LiquidityScore × 0.35 + SpreadScore × 0.25
```

### Absolute Scoring (ScoreVenue)

`ScoreVenue(market)` computes standalone scores for a single market without a
comparison partner. This is useful for inspection and for the forced-routing
path (when only one venue is available):

| Sub-score      | Formula                                       | Range |
|----------------|-----------------------------------------------|-------|
| PriceScore     | `clamp(1 − YesMid, 0, 1)`                    | [0,1] |
| SpreadScore    | `clamp(1 − Spread, 0, 1)`                    | [0,1] |
| LiquidityScore | `log(1 + Liquidity) / log(1 + $1M)`          | [0,1] |

The logarithmic liquidity scale prevents a single venue with extreme depth from
completely dominating; the reference of $1M reflects a well-capitalised
prediction market.

---

## Decision Flow

```
Input: MatchResult  (MarketA + MarketB)
       OrderSide    ("yes" or "no")
       OrderSize    ($ amount)
           │
           ▼
Step 1: Staleness check
    ─ FetchedAt older than threshold (default 2m)?
    ─ Yes → add warning; continue
           │
           ▼
Step 2: Availability check
    ─ Both Status != "open" → return EquinoxError (cannot route)
    ─ One unavailable       → forcedDecision() to remaining venue
    ─ Both open             → continue
           │
           ▼
Step 3: Cross-venue min-max scoring
    ─ scoreVenuePair(marketA, marketB, side)
    ─ Produces VenueScore for each with all three sub-scores
           │
           ▼
Step 4: Mixed-signals detection
    ─ Do Price, Liquidity, Spread all favour the same venue?
    ─ No → add "mixed signals" warning; continue
           │
           ▼
Step 5: Winner selection
    ─ diff = |TotalScore_A − TotalScore_B|
    ─ diff > 0.05 → higher score wins (clear decision)
    ─ diff ≤ 0.05 → tie; add warning; pick venue with higher raw Liquidity
           │
           ▼
Step 6: Build RoutingDecision
    ─ RecommendedVenue, Confidence, VenueScores
    ─ Human-readable Reasoning
    ─ Accumulated Warnings
```

---

## Edge Case Handling

### Stale Data

If `time.Now() − FetchedAt > PriceDataStalenessThreshold` (default: 2
minutes), the engine adds a warning to `RoutingDecision.Warnings`:

```
stale data: kalshi price data is 5m23s old (threshold 2m0s)
— scores may not reflect current market
```

The routing decision is still produced — degraded information is better than no
decision — but the warning makes the staleness visible to the caller and the UI.

### One Venue Unavailable

If `Status != "open"` for one market, the engine routes exclusively to the
other with confidence 1.0 (only one option). The unavailable venue receives a
zero-filled `VenueScore`. A warning is added explaining why.

### Both Venues Unavailable

Returns an `EquinoxError` — no routing decision can be produced.

### Tie (scores within 5%)

Binary min-max normalisation means the most common tie scenario arises when one
venue wins on price and the other wins on liquidity, with the spread equal. The
5% threshold (with a 1e-9 float-drift guard) captures this.

Tiebreak rule: **prefer the venue with higher raw `Liquidity`**. Liquidity
depth is the most operationally consequential metric at execution time — a
deeper book means the stated price is more achievable for larger orders.

### Mixed Signals

When different metrics favour different venues (e.g., A has better price, B
has better liquidity), the engine still produces a decision based on the
weighted sum, but adds a warning. This is intentional: the weights encode a
deliberate priority ordering, but the trader should be aware that the venues
are not uniformly better or worse.

---

## Example: Walking Through a Real Routing Decision

**Scenario:** Route $500 Buy Yes for a matched "2026 midterm elections" market.

| Metric       | Kalshi        | Polymarket    |
|--------------|---------------|---------------|
| YesAsk       | 0.46          | 0.52          |
| Spread       | 0.02          | 0.08          |
| Liquidity    | $80,000       | $240,000      |
| FetchedAt    | 90s ago       | 45s ago       |

**Step 1:** Both markets fresh (< 2 minutes). No staleness warning.

**Step 2:** Both open. Proceed to scoring.

**Step 3:** Normalise:

| Sub-score      | Kalshi    | Polymarket |
|----------------|-----------|------------|
| Price (ask)    | 1.0       | 0.0        |
| Spread         | 1.0       | 0.0        |
| Liquidity      | 0.0       | 1.0        |
| **TotalScore** | **0.65**  | **0.35**   |

Kalshi: `1.0×0.40 + 0.0×0.35 + 1.0×0.25 = 0.65`
Polymarket: `0.0×0.40 + 1.0×0.35 + 0.0×0.25 = 0.35`

**Step 4:** Price and spread favour Kalshi; liquidity favours Polymarket →
**mixed signals warning added**.

**Step 5:** diff = 0.30 > 0.05 → Kalshi wins clearly.

**Output:**
```
RecommendedVenue: "kalshi"
Confidence: 0.30
Reasoning: "kalshi wins for $500 YES order (score 0.650 vs polymarket 0.350);
            advantages: better YES price (0.4600 vs 0.5200), tighter spread
            (0.0200 vs 0.0800)"
Warnings:  ["mixed signals: price, liquidity, and spread metrics do not all
             favour the same venue — review scores before routing"]
```

---

## What Was Not Implemented (and Why)

| Feature | Decision |
|---------|----------|
| **Order-size slippage** | Requires a full order-book snapshot, not just a single `Liquidity` scalar. The canonical `Market` struct captures total liquidity; modelling actual slippage curves is out of scope for this prototype. |
| **Fee adjustment** | Kalshi and Polymarket have different fee structures. Without authenticated API access to get per-user fee tiers, including fees would add false precision. |
| **Time-to-resolution discount** | Markets closer to resolution are less valuable to hold. Implementing a time-value adjustment would require a position-sizing model, which is beyond this prototype's scope. |
| **Cross-venue arbitrage detection** | When equivalent markets have significantly different prices (mispricing), the optimal action is arbitrage, not routing. Detecting and executing arb is a separate system. |
| **Multi-venue routing** | The engine assumes exactly two venues. Extending to N venues requires replacing min-max normalisation with a rank-based or linear-programming approach. |
