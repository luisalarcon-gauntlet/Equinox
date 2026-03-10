# CURSOR INSTRUCTIONS — Project Equinox
# This file tells you exactly what to paste into Cursor and when.
# Follow this order precisely.

---

## SETUP: Before Opening Cursor

1. Create a new folder called `equinox` on your computer
2. Copy ALL files from this setup package into `equinox/`:
   - .cursorrules (goes in root of equinox/)
   - .cursor/rules/ (entire folder, goes in root of equinox/)
   - MASTER_PROMPT.md
   - BUILD_ORDER.md
   - docs/ARCHITECTURE.md
   - docs/EQUIVALENCE.md
   - docs/ROUTING.md
3. Open the `equinox/` folder in Cursor (File → Open Folder)
4. Verify Cursor shows the .cursorrules file in the file tree
5. Open Cursor Settings → Features → ensure "Rules for AI" is enabled

---

## HOW TO USE CURSOR FOR THIS PROJECT

### Use Composer (not Chat) for all building tasks
- Open Composer: Cmd+I (Mac) or Ctrl+I (Windows)
- Composer builds across multiple files at once
- Chat is only for quick questions

### Attach context files to every Composer session
When you start a new Composer session, always attach:
- MASTER_PROMPT.md
- The relevant .cursor/rules/*.mdc files for what you're building

---

## PHASE 1: Project Initialization

Open Composer and paste this EXACTLY:

```
Read MASTER_PROMPT.md and BUILD_ORDER.md completely before doing anything.

Then execute Phase 1 of BUILD_ORDER.md:
- Initialize the Go module (github.com/equinox)
- Create the Makefile with dev, build, test, test-coverage, lint, clean targets
- Create the .env.example file
- Install the two dependencies: github.com/google/uuid and github.com/joho/godotenv
- Create the complete folder structure from MASTER_PROMPT.md (empty files are fine)

Do not write any logic yet. Foundation only.
```

Wait for Cursor to finish. Verify the folder structure was created correctly.

---

## PHASE 2: Foundation Packages

Open a new Composer session and paste:

```
Read MASTER_PROMPT.md. Read error-handling.mdc and testing.mdc.

Build Phase 2 of BUILD_ORDER.md — the foundation packages.
Follow TDD strictly: write tests first, then implementation.

Build in this order:
1. errors/errors.go — EquinoxError struct
2. logger/logger.go — Info, Warn, Error methods
3. config/config.go — load from environment, sensible defaults
4. models/market.go — Market, MatchResult, VenueScore, RoutingDecision structs

For each: write the test file first, confirm it fails, then implement to make it pass.
All tests must be green before moving to the next package.
```

---

## PHASE 3: Venue Connectors

Open a new Composer session and paste:

```
Read MASTER_PROMPT.md. Read api-clients.mdc, error-handling.mdc, testing.mdc.

Build Phase 3 of BUILD_ORDER.md — the venue connectors.
Follow TDD strictly.

First, research the actual API response shapes:
- Kalshi: https://trading-api.kalshi.com/trade-api/v2/markets
- Polymarket: https://gamma-api.polymarket.com/markets

Then build in this order:
1. venues/connector.go — VenueConnector interface
2. venues/kalshi/models.go — raw struct matching actual API response
3. venues/kalshi/adapter_test.go — write ALL adapter tests first
4. venues/kalshi/adapter.go — implement to pass tests
5. venues/kalshi/client_test.go — write ALL client tests with mock HTTP server
6. venues/kalshi/client.go — implement to pass tests
7. Repeat steps 2-6 for Polymarket

All tests green before moving on.
```

---

## PHASE 4: Equivalence Detection

Open a new Composer session and paste:

```
Read MASTER_PROMPT.md. Read ai-layer.mdc, architecture.mdc, testing.mdc.
Also read docs/EQUIVALENCE.md — this explains the full approach in detail.

Build Phase 4 of BUILD_ORDER.md — the equivalence detection system.
Follow TDD strictly. Tests before every implementation file.

Build in this order:
1. equivalence/tools/check_opposites.go
2. equivalence/tools/check_synonyms.go
3. equivalence/tools/check_entity_match.go
4. equivalence/tools/check_date_alignment.go
5. equivalence/tools/check_structural.go
6. equivalence/heuristic.go
7. ai/client.go — Anthropic API client with tool support
8. equivalence/detector.go — orchestrates heuristic + AI

For each tool: write test first, implement, all tests green.
The detector must run heuristics first. AI is fallback only.
AI fallback must degrade gracefully if Anthropic is unavailable.
```

---

## PHASE 5: Routing Engine

Open a new Composer session and paste:

```
Read MASTER_PROMPT.md. Read architecture.mdc, testing.mdc.
Also read docs/ROUTING.md — this explains the full routing logic in detail.

Build Phase 5 of BUILD_ORDER.md — the routing engine.
Follow TDD strictly.

Build routing/engine.go:
- Write routing/engine_test.go FIRST with ALL test cases from testing.mdc
- Implement Route() and ScoreVenue() to pass all tests
- Weights: Price 40%, Liquidity 35%, Spread 25%
- Handle all edge cases: stale data, unavailable venue, tie, mixed signals
- Every RoutingDecision must have non-empty Reasoning
- Stale data and mixed signals must add entries to Warnings

All tests green before moving on.
```

---

## PHASE 6: HTTP Server + UI

Open a new Composer session and paste:

```
Read MASTER_PROMPT.md. Read architecture.mdc.

Build Phase 6 and 7 of BUILD_ORDER.md — the HTTP server and UI.

1. Build server/server.go:
   - GET /health → {"status":"ok"}
   - GET /search?q={query} → fetch from Kalshi + Polymarket in parallel (goroutines)
     → run equivalence detection on all pairs → return []MatchResult as JSON
   - POST /route → parse {market_id, side, size} → return RoutingDecision as JSON
   - GET / → serve embedded static/index.html
   - Write server tests first

2. Build static/index.html (embedded in binary via go:embed):
   - Search input + button
   - Results grid: matched market pairs side by side
     * Show: title, venue badge, YesMid price, spread, liquidity, confidence, method
     * Highlight if are_opposites is true
   - Route panel: side selector (Yes/No), size input, Route button
   - Decision display: recommended venue highlighted, score breakdown table,
     reasoning text, warnings in yellow
   - Health indicator on page load
   - Use fetch() to call /search and /route
   - Clean minimal styling — readability over beauty

3. Build main.go — wire everything together
```

---

## PHASE 7: Documentation + Final Polish

Open a new Composer session and paste:

```
Read MASTER_PROMPT.md. Read all docs/ files.

Complete Phase 8, 9, and 10 of BUILD_ORDER.md:

1. Write README.md with:
   - Project overview
   - Prerequisites
   - Setup instructions (cp .env.example .env, add API key, make dev)
   - All make commands explained
   - Demo walkthrough
   - Link to docs/ for architecture, equivalence, routing details
   - AI usage disclosure: "Claude Sonnet (claude-sonnet-4-20250514) is used
     as the AI fallback layer in equivalence detection. It is invoked only
     when heuristic confidence falls below 0.80. All AI tool calls are logged
     with their results and reasoning."

2. Write integration tests in tests/integration/full_flow_test.go
   - All test cases from testing.mdc integration section
   - Use mock HTTP servers for venue APIs

3. Run: make test
   Ensure all tests pass.

4. Run: make build
   Ensure binary compiles and runs correctly.

5. Fix any issues found during the above.
```

---

## VERIFICATION: Final Demo Run

After all phases complete, run this verification:

```bash
# 1. All tests pass
make test

# 2. Coverage report
make test-coverage

# 3. Binary builds
make build

# 4. Run from binary (not go run)
./equinox

# 5. Open browser to localhost:8080
# 6. Search "2026 midterm elections"
# 7. Verify matched pairs appear from both venues
# 8. Select a pair and route $500 Buy Yes
# 9. Verify routing decision shows reasoning and venue scores
```

---

## TROUBLESHOOTING

### "Cursor keeps breaking layer separation"
Add this to your Composer message:
"CRITICAL: The routing engine must NEVER import any packages from venues/.
It can only import models/. Re-check all imports."

### "Tests are being written after implementation"
Add this to your Composer message:
"STOP. Delete the implementation file. Write the test file first.
The test must FAIL before you write any implementation. TDD strictly."

### "Error handling is inconsistent"
Add this to your Composer message:
"Read error-handling.mdc. Every error in this codebase must use
EquinoxError. Find all places returning raw errors and fix them."

### "Anthropic API isn't working"
- Verify ANTHROPIC_API_KEY is set in your .env file
- The system should degrade gracefully — heuristic-only results with a warning
- Check logs for [ERROR][ai][anthropic] messages

### "Kalshi or Polymarket API returns unexpected data"
- The RawData field in Market struct preserves the original response
- Check logger output for [WARN][normalizer][venue] messages
- Adapter gracefully handles missing fields — check adapter_test.go for expected behavior
