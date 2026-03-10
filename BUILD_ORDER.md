# BUILD ORDER — Project Equinox
# Follow this order exactly. Do not skip steps. Do not build ahead.
# Each step builds on the previous one.
# TDD: Write tests first for every component before implementation.

---

## BEFORE YOU START
1. Read MASTER_PROMPT.md completely
2. Read all .cursor/rules/*.mdc files
3. Understand the layer separation — it is strictly enforced
4. Confirm Go 1.22 is installed: go version

---

## PHASE 1: Project Foundation

### Step 1.1 — Initialize Go Module
```bash
mkdir equinox && cd equinox
go mod init github.com/equinox
```

### Step 1.2 — Create Makefile
Create Makefile with these targets:
```makefile
.PHONY: dev build test test-coverage lint clean

dev:
	go run main.go

build:
	go build -o equinox main.go

test:
	go test ./... -v

test-coverage:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out

lint:
	go vet ./...

clean:
	rm -f equinox coverage.out
```

### Step 1.3 — Create .env.example
```
ANTHROPIC_API_KEY=your_anthropic_api_key_here
SERVER_PORT=8080
HTTP_TIMEOUT=10s
KALSHI_BASE_URL=https://trading-api.kalshi.com/trade-api/v2
POLYMARKET_BASE_URL=https://gamma-api.polymarket.com
HEURISTIC_CONFIDENCE_THRESHOLD=0.80
PRICE_STALENESS_THRESHOLD=2m
```

### Step 1.4 — Install Dependencies
```bash
go get github.com/google/uuid        # for generating market IDs
go get github.com/joho/godotenv      # for loading .env file
```

---

## PHASE 2: Foundation Packages (No External Calls Yet)

### Step 2.1 — errors/errors.go
Write tests first: errors/errors_test.go
Then implement:
- EquinoxError struct with Layer, Venue, Message, Err fields
- Error() string method
- Unwrap() error method
Tests must pass before moving on.

### Step 2.2 — logger/logger.go
Write tests first: logger/logger_test.go
Then implement:
- Logger struct
- Info(layer, venue, message string) method
- Warn(layer, venue, message string, err error) method
- Error(layer, venue, message string, err error) method
- Output format: [INFO][layer][venue] message
Tests must pass before moving on.

### Step 2.3 — config/config.go
Write tests first: config/config_test.go
Then implement:
- Config struct with all fields (see architecture.mdc)
- Load() function that reads from environment variables
- Sensible defaults for all optional fields
- Return EquinoxError if ANTHROPIC_API_KEY is missing
Tests must pass before moving on.

### Step 2.4 — models/market.go
Write tests first: models/market_test.go
Then implement:
- Market struct (exact fields from MASTER_PROMPT.md)
- MatchResult struct
- VenueScore struct
- RoutingDecision struct
Tests must pass before moving on.

---

## PHASE 3: Venue Connectors

### Step 3.1 — venues/connector.go
Implement VenueConnector interface:
```go
type VenueConnector interface {
    FetchMarkets(ctx context.Context, query string) ([]models.Market, error)
    GetVenueName() string
}
```
No tests needed for interface definition.

### Step 3.2 — venues/kalshi/models.go
Write tests first: venues/kalshi/models_test.go
Then implement KalshiMarket raw struct mirroring actual Kalshi API response.
Research actual Kalshi API response shape at:
https://trading-api.kalshi.com/trade-api/v2/markets
All fields must have JSON tags.

### Step 3.3 — venues/kalshi/adapter.go
Write tests first: venues/kalshi/adapter_test.go
Test cases from testing.mdc:
- TestKalshiAdapterNormalizesPrice
- TestKalshiAdapterCalculatesSpread
- TestKalshiAdapterCalculatesMidpoint
- TestKalshiAdapterHandlesMissingFields
- TestKalshiAdapterParsesDate
- TestKalshiAdapterSetsVenue
- TestKalshiAdapterPreservesRawData
- TestKalshiAdapterSetsCategory
Then implement AdaptKalshiMarket(raw KalshiMarket) (models.Market, error)
All tests must pass before moving on.

### Step 3.4 — venues/kalshi/client.go
Write tests first using mock HTTP server: venues/kalshi/client_test.go
Test cases from testing.mdc:
- TestKalshiFetchReturnsMarkets
- TestKalshiFetchHandlesTimeout
- TestKalshiFetchHandlesNon200
- TestKalshiFetchHandlesMalformedJSON
- TestKalshiFetchHandlesEmptyResponse
Then implement KalshiClient with FetchMarkets and GetVenueName.
All tests must pass before moving on.

### Step 3.5 — venues/polymarket/models.go
Same pattern as Step 3.2 but for Polymarket.
Research actual Polymarket API response shape at:
https://gamma-api.polymarket.com/markets

### Step 3.6 — venues/polymarket/adapter.go
Same pattern as Step 3.3 but for Polymarket.
Test cases from testing.mdc (polymarket adapter tests).
Note: Polymarket outcomePrices is a []string — parse [0] as float64 for YesPrice.

### Step 3.7 — venues/polymarket/client.go
Same pattern as Step 3.4 but for Polymarket.
All tests must pass before moving on.

---

## PHASE 4: Equivalence Detection

### Step 4.1 — equivalence/tools/check_opposites.go
Write tests first: equivalence/tools/check_opposites_test.go
Test cases:
- TestCheckOppositesDetectsPartyOpposites
- TestCheckOppositesDetectsPriceOpposites
- TestCheckOppositesDetectsYesNoOpposites
- TestCheckOppositesReturnsFalseUnrelated
- TestCheckOppositesConfidenceInRange
Then implement. All tests pass before moving on.

### Step 4.2 — equivalence/tools/check_synonyms.go
Write tests first: equivalence/tools/check_synonyms_test.go
Test cases:
- TestCheckSynonymsDetectsPartyNames
- TestCheckSynonymsDetectsFinancialTerms
- TestCheckSynonymsCaseInsensitive
- TestCheckSynonymsReturnsFalseNoSynonyms
Then implement with synonym map. All tests pass before moving on.

### Step 4.3 — equivalence/tools/check_entity_match.go
Write tests first: equivalence/tools/check_entity_match_test.go
Test cases:
- TestCheckEntityMatchHighOverlap
- TestCheckEntityMatchLowOverlap
- TestCheckEntityMatchNoOverlap
- TestCheckEntityMatchExtractsNumbers
- TestCheckEntityMatchExtractsOrgs
Then implement. All tests pass before moving on.

### Step 4.4 — equivalence/tools/check_date_alignment.go
Write tests first: equivalence/tools/check_date_alignment_test.go
Test cases:
- TestCheckDateAlignmentExactMatch
- TestCheckDateAlignmentOneDayApart
- TestCheckDateAlignmentMonthsApart
- TestCheckDateAlignmentMissingDate
Then implement. All tests pass before moving on.

### Step 4.5 — equivalence/tools/check_structural.go
Write tests first: equivalence/tools/check_structural_test.go
Then implement structural question comparison.
All tests pass before moving on.

### Step 4.6 — equivalence/heuristic.go
Write tests first: equivalence/heuristic_test.go
Test cases from testing.mdc (all heuristic tests).
Then implement:
- Title normalization
- Entity extraction
- Date comparison
- Confidence scoring
- Returns MatchResult with Method="heuristic"
All tests pass before moving on.

### Step 4.7 — ai/client.go
Write tests first using mock HTTP server: ai/client_test.go
Then implement AnthropicClient:
- NewAnthropicClient(cfg, logger) — returns error if no API key
- EvaluateEquivalence(ctx, marketA, marketB, toolResults) — calls Claude
- Builds prompt with tool results
- Parses JSON response into EquivalenceResult
- Graceful fallback if API unavailable
All tests pass before moving on.

### Step 4.8 — equivalence/detector.go
Write tests first: equivalence/detector_test.go
Then implement Detector:
- Run heuristic first
- If confidence >= threshold → return, log "heuristic"
- Else → run AI tools in parallel, call Claude, return, log "heuristic+ai"
- If AI unavailable → return heuristic result with warning
All tests pass before moving on.

---

## PHASE 5: Routing Engine

### Step 5.1 — routing/engine.go
Write tests first: routing/engine_test.go
Test cases from testing.mdc (all routing tests).
Then implement:
- ScoreVenue(market Market) VenueScore
- Route(matchResult MatchResult, side string, size float64) RoutingDecision
- Weighted scoring: Price 40%, Liquidity 35%, Spread 25%
- Staleness detection → warnings
- Tie breaking → higher liquidity wins
- Always populate Reasoning and Warnings
All tests must pass before moving on.

---

## PHASE 6: HTTP Server

### Step 6.1 — server/server.go
Write tests first: server/server_test.go
Then implement:
- NewServer(cfg, connectors, detector, router, logger)
- GET /health
- GET /search?q={query} → fetch from all venues in parallel → detect matches → return JSON
- POST /route → parse body → route decision → return JSON
- GET / → serve embedded index.html
All tests pass before moving on.

---

## PHASE 7: Static UI

### Step 7.1 — static/index.html
Single HTML file. No external frameworks required.
Must include:
- Search input and button
- Results section showing matched market pairs side by side
  - For each pair: titles, venue badges, YesMid price, Spread, Liquidity, FetchedAt
  - Confidence score and match method (heuristic vs ai)
  - Are opposites indicator if applicable
- Route panel (appears after selecting a match)
  - Order side selector (Yes / No)
  - Order size input ($)
  - Route button
- Routing decision display
  - Recommended venue (highlighted)
  - Score breakdown table (Price, Spread, Liquidity, Total per venue)
  - Reasoning text
  - Warnings (if any, highlighted in yellow)
- Health indicator (calls /health on load)
Use fetch() to call /search and /route.
Style: clean, minimal, readable. Dark or light — your choice.

---

## PHASE 8: Main Entry Point

### Step 8.1 — main.go
Wire everything together:
```go
func main() {
    // 1. Load config
    // 2. Init logger
    // 3. Init venue connectors (Kalshi + Polymarket)
    // 4. Init AI client
    // 5. Init equivalence detector
    // 6. Init routing engine
    // 7. Init and start HTTP server
    // 8. Log startup message with port
}
```

---

## PHASE 9: Documentation

### Step 9.1 — docs/ARCHITECTURE.md
Document:
- System overview and purpose
- Layer diagram (text-based)
- Key design decisions and why
- Technology choices and justifications
- Known limitations and tradeoffs
- How to add a new venue (demonstrates extensibility)

### Step 9.2 — docs/EQUIVALENCE.md
Document:
- What "equivalent" means in this context
- Heuristic approach — steps and scoring
- AI tool layer — each tool described
- Hybrid flow — when each layer activates
- Example: walking through a real match decision
- Limitations and edge cases

### Step 9.3 — docs/ROUTING.md
Document:
- Routing problem definition
- Metrics used and why
- Weight justifications
- Scoring algorithm
- Edge case handling (stale data, unavailable venue, tie)
- Example: walking through a real routing decision
- What was not implemented and why

### Step 9.4 — README.md
Must include:
- Project overview (2-3 sentences)
- Prerequisites (Go 1.22, Anthropic API key)
- Setup instructions:
  ```bash
  git clone ...
  cd equinox
  cp .env.example .env
  # Add your ANTHROPIC_API_KEY to .env
  make dev
  # Open localhost:8080
  ```
- Make commands explained
- Demo walkthrough
- Architecture overview (brief, link to docs/)
- AI usage disclosure (required by brief)
- Known limitations

---

## PHASE 10: Final Polish

### Step 10.1 — Integration Tests
Write and run: tests/integration/full_flow_test.go
All integration test cases from testing.mdc.

### Step 10.2 — Final Test Run
```bash
make test
make test-coverage
```
All tests green. Coverage meets targets.

### Step 10.3 — Build and Verify
```bash
make build
./equinox
# Open localhost:8080
# Run full demo flow manually
```

### Step 10.4 — Demo Preparation
Practice this exact demo flow:
1. Open localhost:8080
2. Search "2026 midterm elections"
3. Show matched pairs with scores
4. Select a pair, route $500 Buy Yes
5. Show routing decision with reasoning
6. Show a warning case (stale data or mixed signals)
7. Run make test-coverage and show coverage report

---

## DONE CHECKLIST
- [ ] make dev starts server successfully
- [ ] Search returns matched markets from both venues
- [ ] Routing decision includes reasoning and warnings
- [ ] make test — all tests pass
- [ ] make test-coverage — 90%+ on core layers
- [ ] make build — produces single binary
- [ ] Binary runs without any external files
- [ ] README has one-command setup
- [ ] docs/ has all three explanation documents
- [ ] .env.example present with all variables
- [ ] AI usage documented in README
