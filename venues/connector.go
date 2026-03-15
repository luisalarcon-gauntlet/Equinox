package venues

import (
	"context"

	"github.com/equinox/models"
)

// MaxMarketsPerVenue is the maximum number of canonical markets each venue
// client should return for a single search workflow.
const MaxMarketsPerVenue = 25

// VenueConnector is the interface every venue client must implement.
// The routing engine and server work exclusively against this interface —
// never against concrete Kalshi or Polymarket types. This keeps the
// routing layer venue-agnostic and allows mock implementations in tests.
type VenueConnector interface {
	// FetchMarkets retrieves markets matching the optional query string and
	// returns them as canonical Market structs ready for the equivalence
	// detector and routing engine.
	FetchMarkets(ctx context.Context, query string) ([]models.Market, error)

	// GetVenueName returns the lowercase venue identifier used throughout
	// the system ("kalshi" or "polymarket").
	GetVenueName() string
}
