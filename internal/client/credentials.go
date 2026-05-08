package client

import (
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
	parsedID, parseErr := strconv.Atoi(rawID)
	if parseErr != nil {
		return 0, "", safety.NewMissingCredentials(
			"TG_API_ID must be an integer (got " + strconv.Quote(rawID) + "). " +
				"Register an app at https://my.telegram.org/apps",
		)
	}
	if parsedID == 0 || apiHash == "" {
		return 0, "", safety.NewMissingCredentials(
			"TG_API_ID and TG_API_HASH must be set as env vars or in .env. " +
				"Register a personal app at https://my.telegram.org/apps",
		)
	}
	return parsedID, apiHash, nil
}
