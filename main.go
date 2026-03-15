// Package main is the entry point for Project Equinox.
// It wires together all layers — configuration, logging, venue connectors,
// equivalence detection, routing engine, and HTTP server — then starts
// listening for requests.
//
// The binary is self-contained: the static UI (static/index.html) is embedded
// at compile time via go:embed and served from memory, so no external files
// are required at runtime.
package main

import (
	"embed"
	"fmt"
	"io/fs"
	"os"

	"github.com/joho/godotenv"

	aipackage "github.com/equinox/ai"
	"github.com/equinox/config"
	"github.com/equinox/equivalence"
	"github.com/equinox/logger"
	"github.com/equinox/routing"
	"github.com/equinox/server"
	"github.com/equinox/venues"
	"github.com/equinox/venues/kalshi"
	kalshidbpkg "github.com/equinox/venues/kalshidb"
	"github.com/equinox/venues/polymarket"
)

// staticFiles embeds the UI at compile time so the binary ships without
// any external file dependencies.
//
//go:embed static/index.html
var staticFiles embed.FS

func main() {
	// Load .env if present. Production deployments set env vars directly;
	// .env is a developer convenience only. We silently ignore the error
	// (file may not exist, and that is fine).
	_ = godotenv.Load()

	// ── 1. Config ────────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "equinox: config error: %v\n", err)
		os.Exit(1)
	}

	// ── 2. Logger ────────────────────────────────────────────────────────────
	log := logger.Default()

	// ── 3. Venue connectors ──────────────────────────────────────────────────
	kalshiClient, err := kalshi.NewKalshiClient(cfg, log)
	if err != nil {
		log.Error("main", "kalshi", "failed to initialise Kalshi client", err)
		os.Exit(1)
	}

	// Build the optional KalshiDB client. When configured it is shared by both
	// venue connectors: Kalshi uses it for event search + match enrichment;
	// Polymarket uses it for DB-backed search + CLOB pricing.
	var dbClient *kalshidbpkg.Client
	if cfg.KalshiDBAPIKey != "" {
		dbClient = kalshidbpkg.NewClient(cfg.KalshiDBBaseURL, cfg.KalshiDBAPIKey, cfg.HTTPTimeout, log)
		log.Info("main", "", fmt.Sprintf("polymarketdb: enabled (base=%s, min_confidence=%.2f)",
			cfg.KalshiDBBaseURL, cfg.MatchesMinConfidence))
	}

	polyClient := polymarket.NewPolymarketClient(cfg, log, dbClient)

	connectors := []venues.VenueConnector{kalshiClient, polyClient}

	// ── 4. AI client ─────────────────────────────────────────────────────────
	aiClient, err := aipackage.NewOpenAIClient(cfg, log, "")
	if err != nil {
		log.Error("main", "openai", "failed to initialise AI client", err)
		os.Exit(1)
	}

	// ── 5. Equivalence detector ───────────────────────────────────────────────
	detector := equivalence.NewDetector(cfg.HeuristicConfidenceThreshold, aiClient, log)

	// ── 6. Routing engine ─────────────────────────────────────────────────────
	routingEngine := routing.NewEngine(cfg.PriceDataStalenessThreshold, log)

	// ── 7. Static filesystem ──────────────────────────────────────────────────
	// Strip the "static/" prefix so the server sees index.html at the root of
	// the FS rather than at static/index.html. This matches what tests expect.
	staticRoot, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Error("main", "", "failed to sub static filesystem", err)
		os.Exit(1)
	}

	// ── 8. HTTP server ────────────────────────────────────────────────────────
	srv := server.NewServer(staticRoot, connectors, detector, routingEngine, log)

	addr := ":" + cfg.ServerPort
	log.Info("main", "", fmt.Sprintf("Equinox starting — http://localhost%s", addr))
	log.Info("main", "", fmt.Sprintf(
		"venues: kalshi=%s  polymarket=%s",
		cfg.KalshiBaseURL, cfg.PolymarketBaseURL,
	))
	if cfg.KalshiDBAPIKey != "" {
		log.Info("main", "", fmt.Sprintf("kalshidb: enabled (base=%s)", cfg.KalshiDBBaseURL))
	}
	log.Info("main", "", fmt.Sprintf(
		"heuristic threshold=%.2f  staleness=%s  timeout=%s",
		cfg.HeuristicConfidenceThreshold,
		cfg.PriceDataStalenessThreshold,
		cfg.HTTPTimeout,
	))

	if err := srv.Start(addr); err != nil {
		log.Error("main", "", "server exited with error", err)
		os.Exit(1)
	}
}
