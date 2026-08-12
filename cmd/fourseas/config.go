package main

import (
	"os"
	"strconv"

	"github.com/joho/godotenv"
	plaidprovider "github.com/kyle-cheung/fourseas/providence/internal/provider/plaid"
)

// Default file locations. Both are gitignored.
const (
	defaultDBPath     = "data/fourseas.duckdb"
	defaultTokensPath = ".secrets/tokens.json"
	defaultLinkPort   = 8080
)

// settings is everything fourseas reads from the environment.
type settings struct {
	plaid      plaidprovider.Config
	dbPath     string
	tokensPath string
}

// loadSettings reads .env, then the process environment, and applies defaults.
// A missing .env file is not an error.
func loadSettings() settings {
	godotenv.Load()

	return settings{
		plaid: plaidprovider.Config{
			ClientID:    os.Getenv("PLAID_CLIENT_ID"),
			Secret:      os.Getenv("PLAID_SECRET"),
			Env:         envOr("PLAID_ENV", "sandbox"),
			RedirectURI: os.Getenv("PLAID_REDIRECT_URI"),
			LinkPort:    intEnvOr("FOURSEAS_LINK_PORT", defaultLinkPort),
		},
		dbPath:     envOr("FOURSEAS_DB_PATH", defaultDBPath),
		tokensPath: envOr("FOURSEAS_TOKENS_PATH", defaultTokensPath),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func intEnvOr(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
