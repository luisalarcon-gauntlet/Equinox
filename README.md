# Project Equinox

Cross-venue prediction market aggregation and intelligent routing prototype.
Equinox connects to Kalshi and Polymarket, detects equivalent markets across
both venues using a hybrid heuristic + AI approach, and recommends which
venue to use for a hypothetical order based on price, liquidity, and spread.

> **This is not a trading product.** No real money, no real orders, no wallets.
> This is infrastructure research and a technical prototype.

---

## Prerequisites

- **Go 1.22+** — [Download](https://go.dev/dl/)
- **Anthropic API key** — Required for the AI fallback layer in equivalence
  detection. Get one at [console.anthropic.com](https://console.anthropic.com).

---

## Setup

```bash
git clone https://github.com/equinox/equinox
cd equinox

# Copy the example env file and add your API key
cp .env.example .env
# Edit .env and set ANTHROPIC_API_KEY=your_key_here

# Start the development server
make dev

# Open the UI
open http://localhost:8080
```

That is the complete setup. The binary embeds the UI at compile time — no
static files, no CDN, no additional configuration required.

---

## Make Commands

| Command | Description |
|---------|-------------|
| `make dev` | Run the server from source (`go run main.go`). Reloads on restart. |
| `make build` | Compile a self-contained binary at `./equinox`. |
| `make test` | Run the full test suite with verbose output (`go test ./... -v`). |
| `make test-coverage` | Generate a coverage report and open it in your browser. |
| `make lint` | Run `go vet ./...` to catch common errors. |
| `make clean` | Remove the compiled binary and coverage files. |

---

## Demo Walkthrough

**1. Start the server**
```bash
make dev
# [INFO][main][] Equinox starting — http://localhost:8080
```

**2. Open the UI at [localhost:8080](http://localhost:8080)**

You will see:
- A health indicator (green dot — server is up)
- A search bar
- An empty results area

**3. Search for a market**

Type `2026 midterm elections` and click Search.

Equinox fetches markets from both Kalshi and Polymarket in parallel, runs
equivalence detection on all cross-venue pairs, and displays matched pairs
side-by-side with:
- Both market titles and venue badges
- Yes price, spread, and liquidity for each venue
- Match confidence and detection method (`heuristic` or `heuristic+ai`)
- An "OPPOSITES" badge if the markets are mirror images of the same event

**4. Route a hypothetical order**

Click on a matched pair, choose side (Yes / No) and size ($500), then click
Route. Equinox will display:

```
Route to Kalshi
Score: 0.650 vs Polymarket 0.350

Advantages: better YES price (0.4600 vs 0.5200), tighter spread (0.0200 vs 0.0800)

⚠ Mixed signals: price, liquidity, and spread metrics do not all favour
  the same venue — review scores before routing
```

**5. Trigger a warning case**

Stale data warnings appear automatically when market data is more than 2
minutes old. To see one, wait 2 minutes after searching and route again —
you will see a staleness warning alongside the routing decision.

**6. Check coverage**
```bash
make test-coverage
# Opens an HTML coverage report in your browser
```

---

## Architecture Overview

Equinox has four layers. Layer separation is strict — no layer may import
from a layer above it or call external APIs out-of-turn.

```
Venue Connectors → Canonical Model → Equivalence Detector → Routing Engine
                                           ↓
                                    AI Tool Layer (fallback only)
```

1. **Venue Connectors** (`venues/`) — HTTP clients for Kalshi and Polymarket.
   Raw API responses are adapted into the canonical `Market` struct before
   leaving this layer.

2. **Canonical Model** (`models/`) — Single internal representation.
   `Market`, `MatchResult`, `VenueScore`, `RoutingDecision`.

3. **Equivalence Detector** (`equivalence/`) — Heuristic matcher runs first.
   If confidence ≥ 0.80, the result is returned immediately. Below that
   threshold, five specialised tools run in parallel and Claude synthesises
   the evidence.

4. **Routing Engine** (`routing/`) — Scores each venue on price (40%),
   liquidity (35%), and spread (25%) using cross-venue min-max normalisation.
   Produces a `RoutingDecision` with human-readable reasoning and warnings.

5. **HTTP Server** (`server/`) — Thin handlers. No business logic. Fetches
   both venues in parallel on `/search`. Caches matched pairs for `/route`.

Full documentation: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)

---

## AI Usage Disclosure

**Claude Sonnet (`claude-sonnet-4-20250514`) is used as the AI fallback layer
in equivalence detection. It is invoked only when heuristic confidence falls
below 0.80. All AI tool calls are logged with their results and reasoning.**

Specifically:
- The heuristic layer runs on every market pair with zero API cost.
- Only pairs that score below the 0.80 confidence threshold trigger an
  Anthropic API call.
- Before calling Claude, five deterministic tools run in parallel and produce
  structured evidence (entity overlap scores, date alignment, synonym
  detection, opposite detection, structural comparison).
- Claude receives the pre-computed tool results and synthesises them into a
  final equivalence decision — it is not reasoning from scratch.
- Every AI call is logged: `[INFO][ai][anthropic] evaluating equivalence: '...' vs '...'`
- The result includes the `Method` field (`"heuristic+ai"`) so every decision
  is fully attributable.
- If the Anthropic API is unavailable, the system degrades gracefully to
  heuristic-only results with a warning — it never fails silently.

---

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ANTHROPIC_API_KEY` | **Yes** | — | Anthropic API key for Claude |
| `SERVER_PORT` | No | `8080` | HTTP server port |
| `HTTP_TIMEOUT` | No | `10s` | Timeout for outbound HTTP calls |
| `KALSHI_BASE_URL` | No | Kalshi production API | Override for testing |
| `POLYMARKET_BASE_URL` | No | Polymarket Gamma API | Override for testing |
| `HEURISTIC_CONFIDENCE_THRESHOLD` | No | `0.80` | Min confidence before AI fallback |
| `PRICE_STALENESS_THRESHOLD` | No | `2m` | Age before a staleness warning is added |

---

## Known Limitations

| Limitation | Notes |
|------------|-------|
| **In-memory match cache** | Resets on each `/search`. A production system would use a persistent store. |
| **O(n²) pair evaluation** | Every Kalshi × Polymarket pair is checked. Acceptable for prototype query sizes (10–50 results). |
| **No authentication** | Uses only public API endpoints. Authenticated endpoints expose richer order-book data. |
| **English only** | All title normalisation and entity extraction assumes ASCII English. |
| **No order-size slippage** | Routing uses total liquidity, not a full order-book snapshot. Real slippage modelling is out of scope. |
| **No fee adjustment** | Kalshi and Polymarket have different fee structures that would require authenticated per-user data. |

---

## Detailed Documentation

- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — Full layer diagram, design decisions, and extensibility guide
- [docs/EQUIVALENCE.md](docs/EQUIVALENCE.md) — Heuristic + AI detection flow, each tool explained, worked example
- [docs/ROUTING.md](docs/ROUTING.md) — Scoring algorithm, weight justifications, edge cases, worked example
