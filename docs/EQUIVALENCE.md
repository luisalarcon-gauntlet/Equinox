# Equivalence Detection — Project Equinox

## What "Equivalent" Means in This Context

Two prediction markets are **equivalent** when they resolve based on the same
real-world event or outcome, regardless of which venue hosts them or how the
question is worded.

Equivalence is not a binary property — it exists on a spectrum:

| Type | Example | Handling |
|------|---------|----------|
| **Identical** | "Will Democrats win the House in 2026?" (both venues) | Heuristic detects immediately |
| **Paraphrase** | "Democrats House majority 2026" vs "Will Dems control House after midterms?" | Heuristic entity overlap |
| **Synonymic** | "GOP control Senate" vs "Republicans Senate majority" | AI synonym tool |
| **Opposite** | "Will Republicans win House?" vs "Will Democrats win House?" | AI opposites tool → `AreOpposites=true` |
| **Unrelated** | "Bitcoin above $100k" vs "Democrats win House" | Both layers reject |

---

## Heuristic Approach

The heuristic matcher (`equivalence/heuristic.go`) runs first on every pair.
It is fast (microseconds), deterministic, and costs nothing.

### Step 1: Title Normalisation

Both market titles are normalised before any comparison:

```
"Will Democrats Control the House? (2026)" 
→ lowercase:  "will democrats control the house 2026"
→ no punct:   "will democrats control the house 2026"
→ collapse ws: "will democrats control the house 2026"
```

This removes surface differences in capitalisation, punctuation, and spacing
that have no semantic meaning.

### Step 2: Token-Level Comparison

The normalised titles are split into word tokens. Token overlap is scored
using a Jaccard-like measure:

```
shared_tokens / max(tokens_A, tokens_B)
```

High overlap (≥ 0.50) contributes the most to the confidence score.

### Step 3: Entity Extraction

Key entities are extracted from both titles:

- **Named entities** — capitalised words, political party names, organisation names
- **Numbers and years** — "2026", "$100k", "50%"
- **Domain-specific keywords** — "house", "senate", "bitcoin", "fed"

Entity overlap is scored independently of raw token overlap, since entity
matches carry more semantic weight than stop-word matches.

### Step 4: Date Proximity

If both markets have a non-zero `ResolvesAt` date, the difference in days is
computed:

| Gap | Score contribution |
|-----|-------------------|
| 0–7 days | High (≥ 0.80 date score) |
| 8–30 days | Medium (≥ 0.50) |
| > 30 days | Low (≤ 0.20) |
| Missing date | Neutral (0.50) — not penalised |

Markets that resolve years apart are very unlikely to be equivalent; a sharp
date-score penalty eliminates most false matches.

### Step 5: Composite Confidence Score

The final confidence is a weighted combination of the individual sub-scores.
Weights reflect empirical importance for prediction market titles:

| Sub-score | Weight | Rationale |
|-----------|--------|-----------|
| Token overlap | 0.40 | Most reliable signal for paraphrase detection |
| Entity overlap | 0.35 | High-precision signal — entities define the event |
| Date proximity | 0.25 | Necessary but not sufficient on its own |

If composite confidence **≥ 0.80**, the pair is marked as a match with
`Method: "heuristic"` and no AI call is made.

If confidence **< 0.80**, the pair is escalated to the AI tool layer.

---

## AI Tool Layer

The AI tool layer (`equivalence/tools/`) consists of five specialised tools
that Claude can use to gather evidence about a market pair. The tools run in
**parallel** (goroutines) so the combined latency is bounded by the slowest
tool rather than the sum.

### Tool 1: `check_opposites`

**Purpose:** Detect markets that are mirror images of the same event.

**Mechanism:** Looks for antonym pairs in normalised titles:
- Political opposites: GOP ↔ Democrats, Republican ↔ Democrat
- Directional opposites: above ↔ below, over ↔ under, rise ↔ fall
- Yes/No inversions: "will X happen" vs "will X fail"

**Output:** `{result: bool, confidence: float64, reasoning: string}`

**Example:**
```
Market A: "will republicans win the house in 2026"
Market B: "will democrats control the house in 2026"
→ result: true, confidence: 0.85
  reasoning: "political party opposites detected (republicans/democrats)"
```

### Tool 2: `check_synonyms`

**Purpose:** Detect equivalent terminology across venues.

**Mechanism:** A curated synonym map covers known domain terms:

| Canonical | Synonyms |
|-----------|---------|
| Democrats | Dems, Democratic Party, Blue |
| Republicans | GOP, Republican Party, Red |
| Federal Reserve | Fed, FOMC, Central Bank |
| Bitcoin | BTC |
| Ethereum | ETH |

Titles are re-scored after synonym substitution. High post-substitution
overlap → synonym match.

**Output:** `{result: bool, confidence: float64, reasoning: string}`

### Tool 3: `check_entity_match`

**Purpose:** Score named entity overlap with higher precision than raw token
matching.

**Mechanism:** Extracts a structured entity set from each title:
- Organisation names (multi-word capitalised sequences)
- Numbers (including "100k", "50%", "2026")
- Domain keywords from a curated list

Entity overlap is scored as `|intersection| / |union|`.

**Output:** `{result: bool, confidence: float64, reasoning: string}`

### Tool 4: `check_date_alignment`

**Purpose:** Compare resolution dates with a configurable tolerance.

**Mechanism:** Computes the absolute day difference between `ResolvesAt`
fields. Within 7 days → aligned. Beyond 30 days → misaligned. Missing
dates → neutral (not penalised, since many venues omit dates for ongoing
markets).

**Output:** `{result: bool, confidence: float64, reasoning: string}`

### Tool 5: `check_structural`

**Purpose:** Compare the grammatical and logical structure of the questions.

**Mechanism:** Analyses question structure patterns:
- Both start with "will X" → strong structural match
- One is "X by date" vs the other is "will X happen before date" → structural match
- One is "price of X" vs "X above Y" → structural mismatch

**Output:** `{result: bool, confidence: float64, reasoning: string}`

---

## Hybrid Flow

```
Input: (marketA from Kalshi, marketB from Polymarket)
           │
           ▼
    ┌──────────────┐
    │  Heuristic   │
    │  Matcher     │
    └──────┬───────┘
           │
    ┌──────▼──────────────────────────┐
    │  Confidence ≥ 0.80?             │
    │  YES → return MatchResult        │◄── Method: "heuristic"
    │  NO  → escalate to AI layer     │
    └──────────────┬──────────────────┘
                   │ (confidence < 0.80)
                   ▼
    ┌─────────────────────────────────────────┐
    │  Run 5 tools in parallel (goroutines)   │
    │  check_opposites                        │
    │  check_synonyms                         │
    │  check_entity_match                     │
    │  check_date_alignment                   │
    │  check_structural                       │
    └──────────────┬──────────────────────────┘
                   │ []ToolResult
                   ▼
    ┌──────────────────────────────────┐
    │  Claude Synthesis                │
    │  (claude-sonnet-4-20250514)      │
    │  Prompt includes:                │
    │   - Both market titles           │
    │   - Resolution dates             │
    │   - All 5 tool results           │
    └──────────────┬───────────────────┘
                   │
    ┌──────────────▼────────────────────────────────┐
    │  Parse JSON response:                          │
    │  {is_equivalent, are_opposites,                │
    │   confidence, reasoning}                       │
    └──────────────┬────────────────────────────────-┘
                   │
                   ▼
    Return MatchResult ◄── Method: "heuristic+ai"
```

### AI Unavailability — Graceful Degradation

If the Anthropic API is unavailable (network error, rate limit, timeout):

1. The detector logs a warning: `"AI layer unavailable — returning heuristic-only result"`
2. Returns the heuristic result with `Method: "heuristic-only"`
3. Adds a warning to `MatchResult.Warnings`
4. **Fallback match threshold:** If heuristic confidence ≥ 0.50 (lower than
   the normal 0.80 threshold), the pair is still returned as a match. This
   avoids false negatives when AI is down.

The degraded result is always surfaced to the caller via the `Method` and
`Warnings` fields — AI unavailability is never silently swallowed.

---

## Example: Walking Through a Real Match Decision

**Pair:** "Will Democrats control the House after 2026 midterms?" (Kalshi)
vs "Will Democrats win a House majority in 2026?" (Polymarket)

**Step 1 — Normalisation:**
```
A: "will democrats control the house after 2026 midterms"
B: "will democrats win a house majority in 2026"
```

**Step 2 — Token overlap:**
- A tokens: {will, democrats, control, the, house, after, 2026, midterms}
- B tokens: {will, democrats, win, a, house, majority, in, 2026}
- Shared: {will, democrats, house, 2026} → 4/8 = 0.50 token overlap

**Step 3 — Entity extraction:**
- A entities: {democrats, house, 2026}
- B entities: {democrats, house, 2026}
- Entity overlap: 3/3 = 1.00

**Step 4 — Date proximity:**
Both markets resolve November 2026 → gap = 0 days → score = 1.00

**Step 5 — Composite:**
```
0.50 × 0.40 + 1.00 × 0.35 + 1.00 × 0.25
= 0.20 + 0.35 + 0.25 = 0.80
```

Confidence = 0.80, which exactly meets the threshold. Result: `IsMatch=true`,
`Method: "heuristic"`, no AI call.

---

## Limitations and Edge Cases

| Edge Case | How Handled |
|-----------|-------------|
| **Short titles** (< 3 tokens) | Token overlap score is penalised; entity score dominates |
| **Different venues, same question** | Heuristic detects with very high confidence |
| **Markets with no date** | Date score set to 0.50 (neutral), not penalised |
| **Abbreviations** (BTC, GOP) | synonym tool replaces before comparison |
| **Opposite markets** | Detected by `check_opposites` tool; `AreOpposites=true` in result |
| **Homonymous entities** | Not handled — "Fed" could be Federal Reserve or Fed Cup. The AI layer's Claude reasoning mitigates this |
| **Non-English titles** | Not supported — all tooling assumes ASCII English |
| **Scalar markets** | Kalshi scalar markets (e.g. "BTC price on Dec 31") may not match binary Polymarket markets; heuristic will likely produce low confidence, triggering AI |
