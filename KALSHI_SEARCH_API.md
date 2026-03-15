# KalshiDB Search API — Contract

## Overview
KalshiDB is an internal search service that indexes all open Kalshi prediction market events.
It provides semantic + keyword search so users can find relevant bets using natural language.
It does **not** return live prices — use the `kalshi_url` in each result to fetch live data directly from Kalshi.

## Base URL
```
http://localhost:8000   # local / docker
https://your-railway-url  # production (set via env)
```

## Authentication
All `/v1/` endpoints require an API key header:
```
X-API-Key: <key>
```

---

## Endpoints

### Search
```
GET /v1/search?q={query}&limit={n}&category={category}
```
| Param | Type | Required | Description |
|---|---|---|---|
| `q` | string | yes | Natural language search query |
| `limit` | int | no | Number of results (default 5, max 20) |
| `category` | string | no | Filter: `Sports`, `Crypto`, `Politics`, `Entertainment`, `World` |

**Response**
```json
{
  "query": "Bitcoin",
  "results": [
    {
      "event_ticker": "KXBTC-26MAR1322",
      "series_ticker": "KXBTC",
      "title": "Bitcoin price range on Mar 13, 2026 at 10pm EDT?",
      "sub_title": "On Mar 13, 2026 at 10pm EDT",
      "category": "Crypto",
      "event_type": "price_threshold",
      "market_count": 75,
      "close_time": "2026-03-13 21:00:00",
      "search_score": 0.030303,
      "match_source": "both",
      "kalshi_url": "https://api.elections.kalshi.com/trade-api/v2/events/KXBTC-26MAR1322?with_nested_markets=true"
    }
  ],
  "meta": {
    "total_results": 5,
    "query_time_ms": 38,
    "cached": false
  }
}
```

### Batch Search
```
POST /v1/search/batch
Content-Type: application/json
```
```json
{
  "queries": ["Bitcoin", "Raptors game", "The Oscars"],
  "limit": 5,
  "category": null
}
```
Returns an array of search responses, one per query, in the same shape as above.

### Health
```
GET /v1/health   (no auth required)
```
```json
{
  "status": "ok",
  "events": {
    "total": 5196,
    "fully_processed": 5196,
    "pending_enrich": 0,
    "pending_embed": 0
  },
  "sync": {
    "last_sync": "2026-03-14T19:51:20Z",
    "last_error": null
  }
}
```

---

## Key Fields
| Field | Description |
|---|---|
| `event_ticker` | Unique Kalshi event ID — use this as the primary key |
| `kalshi_url` | Call this URL to get live prices, all markets, and full event detail |
| `market_count` | Number of yes/no markets within this event |
| `close_time` | When the event closes (UTC) |
| `match_source` | `both`, `keyword`, or `vector` — how it was matched |

## Notes
- The database auto-syncs with Kalshi every 15 minutes — new events become searchable within minutes of opening on Kalshi
- Queries support natural language: `"Raptors"` matches `"Professional Basketball"`, `"Oscars"` matches `"Academy Awards"` etc.
- Results are ranked by relevance (hybrid BM25 + semantic similarity)
