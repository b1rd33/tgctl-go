package client

import (
	"math"
	"os"
	"strconv"

	"github.com/b1rd33/tgctl-go/internal/safety"
)

// EnsureCredentials reads TG_API_ID and TG_API_HASH and returns them, or a
// *safety.MissingCredentials error pointing the user at https://my.telegram.org/apps.
// Mirrors tgcli.client.ensure_credentials.
func EnsureCredentials() (apiID int, apiHash string, err error) {
	rawID := os.Getenv("TG_API_ID")
	if rawID == "" {
		rawID = "0"
	}
	apiHash = os.Getenv("TG_API_HASH")
	parsed, parseErr := strconv.ParseInt(rawID, 10, 64)
	if parseErr != nil {
		return 0, "", safety.NewMissingCredentials(
			"TG_API_ID must be an integer (got " + strconv.Quote(rawID) + "). " +
				"Register an app at https://my.telegram.org/apps",
		)
	}
	if parsed == 0 || apiHash == "" {
		return 0, "", safety.NewMissingCredentials(
			"TG_API_ID and TG_API_HASH must be set as env vars or in .env. " +
				"Register a personal app at https://my.telegram.org/apps",
		)
	}
	if parsed < 0 || parsed > math.MaxInt32 {
		return 0, "", safety.NewMissingCredentials(
			"TG_API_ID must be a positive 32-bit integer (got " + strconv.Quote(rawID) + "). " +
				"Register an app at https://my.telegram.org/apps",
		)
	}
	return int(parsed), apiHash, nil
}

func validateAPIID(apiID int) error {
	if apiID <= 0 || int64(apiID) > math.MaxInt32 {
		return safety.NewMissingCredentials("TG_API_ID must be a positive 32-bit integer")
	}
	return nil
}
