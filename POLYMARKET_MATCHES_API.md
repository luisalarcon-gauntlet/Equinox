# Polymarket + Matches API — Contract

Extends the existing Kalshi Search API with two new capabilities:
1. **Polymarket search** — same semantic + keyword search, but over Polymarket events
2. **Cross-platform matches** — pre-computed links between Kalshi and Polymarket events

The recommended flow is: **Search Kalshi → check matches → fetch live prices from both platforms.**

---

## Base URL / Auth

Same as Kalshi search — no changes:
```
http://localhost:8000        # local / docker
https://your-railway-url     # production

X-API-Key: <PUBLIC_API_KEY>  # same key, same header
```

---

## Recommended Flow

```
1. User query ("NBA Portland game")
        │
        ▼
2. GET /v1/search?q=...
   → returns kalshi_url + event_ticker
        │
        ▼
3. GET /v1/matches/{event_ticker}
   → returns poly_clob_url + confidence
        │
        ├─ match found? → fetch live prices from BOTH:
        │     • Kalshi:     GET {kalshi_url}
        │     • Polymarket: GET {poly_clob_url}
        │
        └─ no match? → fetch Kalshi price only
```

---

## New Endpoints

---

### Polymarket Search
```
GET /v1/polymarket/search?q={query}&limit={n}
```

| Param | Type | Required | Default | Description |
|---|---|---|---|---|
| `q` | string | yes | — | Natural language query |
| `limit` | int | no | 10 | Max results (1–50) |

**Response:**
```json
{
  "query": "NBA",
  "results": [
    {
      "event_id":        "258874",
      "slug":            "nba-por-bkn-2026-03-16",
      "title":           "Trail Blazers vs. Nets",
      "description":     "In the upcoming NBA game...",
      "category":        null,
      "event_type":      "sports_game",
      "tag_labels":      ["Sports", "NBA", "Basketball"],
      "market_count":    1,
      "end_date":        "2026-03-16 18:30:00",
      "search_score":    0.016667,
      "match_source":    "vector",
      "polymarket_url":  "https://polymarket.com/event/nba-por-bkn-2026-03-16",
      "clob_url":        "https://clob.polymarket.com/markets/0x0b2eabd9..."
    }
  ],
  "meta": {
    "total_results": 1,
    "query_time_ms": 3500,
    "cached": false
  }
}
```

**Key fields for Go:**
| Field | Use |
|---|---|
| `event_id` | Polymarket's unique ID — use as key |
| `clob_url` | Call this to fetch live orderbook / prices |
| `polymarket_url` | Deep link for UI |
| `end_date` | When the market closes |
| `match_source` | `"keyword"`, `"vector"`, or `"both"` — search quality indicator |

---

### Polymarket Batch Search
```
POST /v1/polymarket/search/batch
Content-Type: application/json

{
  "queries": ["NBA", "Bitcoin", "Trump"],
  "limit": 5
}
```

Returns one result set per query:
```json
{
  "results": [
    { "query": "NBA",     "results": [...], "meta": {...} },
    { "query": "Bitcoin", "results": [...], "meta": {...} },
    { "query": "Trump",   "results": [...], "meta": {...} }
  ]
}
```

---

### Cross-Platform Matches
```
GET /v1/matches/{kalshi_ticker}?min_confidence={float}
```

| Param | Type | Required | Default | Description |
|---|---|---|---|---|
| `kalshi_ticker` | string | yes | — | Kalshi event ticker from `/v1/search` |
| `min_confidence` | float | no | 0.60 | Filter threshold (0.0–1.0) |

**Response:**
```json
{
  "kalshi_ticker": "KXNBAGAME-26MAR16PORBKN",
  "matches": [
    {
      "kalshi_ticker":  "KXNBAGAME-26MAR16PORBKN",
      "poly_event_id":  "258874",
      "match_type":     "exact",
      "confidence":     0.929,
      "entity_basis":   ["type", "date", "teams", "league"],
      "vector_score":   0.881,
      "entity_score":   1.0,
      "kalshi_title":   "Portland at Brooklyn",
      "poly_title":     "Trail Blazers vs. Nets",
      "kalshi_url":     "https://api.elections.kalshi.com/trade-api/v2/events/KXNBAGAME-26MAR16PORBKN?with_nested_markets=true",
      "poly_clob_url":  "https://clob.polymarket.com/markets/0x0b2eabd9...",
      "event_type":     "sports_game",
      "entity_date":    "2026-03-16",
      "kalshi_closes":  "2026-03-30 23:30:00",
      "poly_closes":    "2026-03-16 18:30:00"
    }
  ],
  "meta": {
    "total_matches": 1,
    "min_confidence": 0.60
  }
}
```

**Key fields for Go:**
| Field | Use |
|---|---|
| `match_type` | `exact` ≥0.90, `related` ≥0.75, `same_topic` ≥0.45 |
| `confidence` | 0.0–1.0 — how certain the match is |
| `kalshi_url` | Call Kalshi API for live Kalshi prices |
| `poly_clob_url` | Call Polymarket CLOB API for live Polymarket prices |
| `entity_date` | Real-world event date (game date, election date) |

**No match = empty array** — `matches: []` — not a 404. Handle gracefully.

---

## Match Types

| `match_type` | `confidence` | Meaning | Recommended action |
|---|---|---|---|
| `exact` | ≥ 0.90 | Same event, same date, same teams | Show both prices |
| `related` | ≥ 0.75 | Very likely the same event | Show both prices |
| `same_topic` | ≥ 0.45 | Same topic, may differ in scope | Show as "related market" |

Use `min_confidence=0.75` if you only want to show prices when the match is high-confidence.

---

## Live Price Fetching (after matching)

Once you have the match, fetch prices directly — **not through this API**:

**Kalshi:**
```
GET {kalshi_url}
# e.g. https://api.elections.kalshi.com/trade-api/v2/events/KXNBAGAME-26MAR16PORBKN?with_nested_markets=true
```
Returns markets with `yes_bid`, `yes_ask`, `no_bid`, `no_ask`.

**Polymarket CLOB:**
```
GET {poly_clob_url}
# e.g. https://clob.polymarket.com/markets/0x0b2eabd9...
```
Returns orderbook with `best_bid`, `best_ask`.

---

## Health Check
```
GET /v1/health   (no auth required)
```
```json
{
  "status": "ok",
  "kalshi":     { "events": { "total": 5985, "fully_processed": 5787 }, "sync": { "last_sync": "..." } },
  "polymarket": { "events": { "total": 8734, "fully_processed": 8734 }, "sync": { "last_sync": "..." } },
  "matches":    { "db_path": "matches.duckdb" },
  "cache":      { "size": 0, "maxsize": 500, "ttl_secs": 60 }
}
```

---

## Error Responses

| Status | Meaning |
|---|---|
| `401` | Missing or invalid `X-API-Key` |
| `422` | Bad query params (e.g. `limit` out of range) |
| `503` | Polymarket or Matches DB not configured (check `POLY_DB_PATH` / `MATCHES_DB_PATH` env vars) |
| `500` | Internal error — check server logs |
