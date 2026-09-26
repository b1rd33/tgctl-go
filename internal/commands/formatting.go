package commands

import (
	"github.com/b1rd33/tgctl-go/internal/safety"
	textutil "github.com/b1rd33/tgctl-go/internal/text"
	"github.com/spf13/cobra"
)

func addEntityFlag(cmd *cobra.Command) {
	cmd.Flags().String("entities", "", "Explicit JSON text entities with UTF-16 offsets (plain text by default)")
}

func commandEntities(cmd *cobra.Command, body string, caption bool) ([]textutil.Entity, error) {
	raw, _ := cmd.Flags().GetString("entities")
	limit := 4096
	if caption {
		limit = 2048
	}
	v, err := textutil.ParseEntities(raw, body, limit)
	if err != nil {
		return nil, safety.NewBadArgs("%s", err)
	}
	return v, nil
}
