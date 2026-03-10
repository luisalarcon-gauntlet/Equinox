# Equivalence Detection — Project Equinox

## The Problem

Prediction markets representing the same real-world event often look
completely different across venues. Consider these two markets:

- Kalshi: "Will the GOP control the House after the 2026 midterms?"
- Polymarket: "Democrats win House majority 2026?"

A naive string comparison would score these as completely unrelated.
Yet they are not just equivalent — they are opposite sides of the same
event. Routing a trade without recognizing this would be a fundamental
failure of the system.

Equivalence detection is the hardest problem in Project Equinox.

---

## What "Equivalent" Means

Two markets are equivalent if they resolve to the same binary outcome
based on the same real-world event at approximately the same time.

This includes:
1. Same question, different wording
   "Will Democrats control the House?" vs "Do Democrats win the House?"

2. Opposite sides of the same event
   "Will GOP win the House?" vs "Will Democrats win the House?"
   (if GOP loses, Democrats win — same event, complementary outcomes)

3. Same entity, different naming
   "Will the Federal Reserve cut rates?" vs "Will the Fed lower rates?"

Markets are NOT equivalent if:
- They refer to different events (even similar-sounding ones)
- They resolve at significantly different times
- They ask about different threshold levels ("above $100k" vs "above $90k")

---

## The Hybrid Approach

Equinox uses a two-layer hybrid approach: deterministic heuristics first,
AI-assisted semantic analysis as fallback.

```
Two markets
    │
    ▼
┌─────────────────────────────────┐
│         HEURISTIC LAYER         │
│  - Normalize text               │
│  - Extract entities             │
│  - Compare dates                │
│  - Score overlap                │
└────────────────┬────────────────┘
                 │
    Confidence >= 0.80?
         │              │
        YES              NO
         │              │
    ✓ Match         ┌───▼────────────────────────────────┐
    confirmed       │         AI TOOL LAYER               │
    Method:         │  Tools run (parallel where possible)│
    "heuristic"     │  - check_opposites                  │
                    │  - check_synonyms                   │
                    │  - check_entity_match               │
                    │  - check_date_alignment             │
                    │  - check_structural_equivalence     │
                    └───────────────┬────────────────────┘
                                    │
                             ┌──────▼──────┐
                             │   Claude    │
                             │  Synthesis  │
                             └──────┬──────┘
                                    │
                            MatchResult returned
                            Method: "heuristic+ai"
```

---

## Layer 1: Heuristic Matching

### Step 1: Title Normalization
Both titles are normalized before comparison:
- Convert to lowercase
- Remove punctuation and special characters
- Collapse multiple whitespace to single space
- Trim leading/trailing whitespace

Example:
- "Will the GOP control the House??" → "will the gop control the house"
- "Democrats win House majority 2026!" → "democrats win house majority 2026"

### Step 2: Entity Extraction
Key entities are extracted from normalized titles:
- Numbers and years (2026, $680, 100k)
- Organization names (GOP, Democrats, Fed, Apple)
- Person names (Trump, Powell, Musk)
- Geographic references (US, House, Senate)

### Step 3: Entity Overlap Scoring
The overlap between entity sets from both markets is scored:
```
overlap = |entities_A ∩ entities_B|
union    = |entities_A ∪ entities_B|
score    = overlap / union  (Jaccard similarity)
```

### Step 4: Date Proximity Scoring
Resolution dates are compared:
- Exact same date: 1.0
- Within 1 day: 0.9
- Within 7 days: 0.7
- Within 30 days: 0.4
- More than 30 days: 0.0

### Step 5: Combined Confidence Score
```
confidence = (entity_overlap * 0.60) + (date_proximity * 0.40)
```

Entity overlap is weighted higher because date proximity alone cannot
confirm equivalence — many markets resolve in the same month.

### Threshold
If confidence >= 0.80 → match confirmed, return, skip AI layer.
If confidence < 0.80 → escalate to AI layer.

---

## Layer 2: AI Tool Layer

### Why Tools Instead of One Prompt?
Rather than asking Claude one vague question ("are these the same?"),
we provide Claude with five discrete tools. Claude decides which tools
are relevant, runs them, and synthesizes the results. This makes the
AI decision transparent — every tool call is logged with its result.

### Tool 1: check_opposites
**Purpose:** Detect when two markets are mirror images of the same event.

**Examples detected:**
- "Will GOP control the House?" vs "Will Democrats control the House?"
  → Same race, complementary outcomes
- "Will stock close above $680?" vs "Will stock close below $680?"
  → Same price level, opposite sides
- "Will X happen?" vs "Will X fail to happen?"
  → Logical negation

**Returns:** are_opposites (bool), confidence (float64), reasoning (string)

### Tool 2: check_synonyms
**Purpose:** Detect equivalent terminology used differently across venues.

**Synonym map includes:**
- Political: GOP = Republicans = Republican Party
- Political: Dems = Democrats = Democratic Party
- Financial: Fed = Federal Reserve = FOMC
- Crypto: BTC = Bitcoin, ETH = Ethereum
- Geographic: US = United States = America
- Generic: win = control = take = secure

**Returns:** synonyms_found ([]pair), confidence (float64)

### Tool 3: check_entity_match
**Purpose:** Score named entity overlap after synonym normalization.

After synonyms are resolved, re-score entity overlap. This catches
cases where the heuristic layer scored low because of synonym mismatch
but entities are actually the same after normalization.

**Returns:** matched_entities ([]string), score (float64)

### Tool 4: check_date_alignment
**Purpose:** Compare resolution dates with configurable tolerance.

Applies the same date proximity scoring as the heuristic layer but
with awareness of context (e.g., an election market resolving "Nov 2026"
and "Nov 3 2026" are clearly the same event).

**Returns:** dates_align (bool), days_apart (int), confidence (float64)

### Tool 5: check_structural_equivalence
**Purpose:** Compare question structure independent of content.

"Will X happen by Y?" and "Does X occur before Y?" have the same
structure. This tool normalizes question patterns to detect structural
matches that indicate same-type questions about the same timeframe.

**Returns:** structurally_equivalent (bool), confidence (float64)

### Claude Synthesis
After tools run, their results are passed to Claude with this context:
- Both market titles
- Both resolution dates
- All tool results with their confidence scores and reasoning

Claude returns:
```json
{
  "is_equivalent": true,
  "are_opposites": false,
  "confidence": 0.91,
  "reasoning": "Both markets ask about Democratic House control in 2026. check_synonyms confirmed 'Democrats' and 'House majority' match across both titles. check_date_alignment confirmed same resolution window."
}
```

---

## Example Walk-Through

**Market A (Kalshi):** "Will the GOP control the House after 2026 midterms?"
**Market B (Polymarket):** "Democrats win House majority 2026?"

**Heuristic Layer:**
- Normalized A: "will the gop control the house after 2026 midterms"
- Normalized B: "democrats win house majority 2026"
- Entities A: {gop, house, 2026, midterms}
- Entities B: {democrats, house, majority, 2026}
- Raw overlap: {house, 2026} = 2 matches
- Jaccard: 2/6 = 0.33 — low overlap (GOP ≠ Democrats without synonyms)
- Date proximity: both resolve Nov 2026 = 0.9
- Combined confidence: (0.33 * 0.60) + (0.90 * 0.40) = 0.558
- 0.558 < 0.80 → escalate to AI layer

**AI Tool Layer:**
- check_opposites → TRUE (0.94) "GOP control = Democrats not winning = same race"
- check_synonyms → found: GOP ↔ Republicans, but no direct Dem synonym hit
- check_entity_match → after synonym resolution: {house, 2026} overlap improved
- check_date_alignment → TRUE (0.90) both resolve Nov 2026
- check_structural_equivalence → TRUE (0.85) both "will X control Y?"

**Claude Synthesis:**
- is_equivalent: TRUE
- are_opposites: TRUE
- confidence: 0.93
- reasoning: "These are opposite sides of the 2026 House control question.
  check_opposites confirmed GOP winning = Democrats losing on the same race.
  Resolution dates align. High confidence these are complementary markets."

**Result:** MatchResult{IsMatch: true, AreOpposites: true, Confidence: 0.93, Method: "heuristic+ai"}

---

## Limitations

1. The synonym map is manually maintained — it covers common political and
   financial terms but will miss domain-specific jargon.

2. The 0.80 heuristic threshold is a starting point — production would
   require tuning against a labeled dataset of known-equivalent markets.

3. Numeric thresholds are not compared — "above $680" and "above $690"
   would score as high-confidence matches when they are distinct markets.
   A numeric comparison tool would address this.

4. AI layer adds latency (~1-2 seconds) — acceptable for a prototype but
   would need caching or background processing in production.
