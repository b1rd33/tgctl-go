package commands

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestAddOutputFlagsAddsJSONAndHumanMutuallyExclusive(t *testing.T) {
	cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
	AddOutputFlags(cmd)
	if cmd.Flags().Lookup("json") == nil {
		t.Fatalf("missing --json")
	}
	if cmd.Flags().Lookup("human") == nil {
		t.Fatalf("missing --human")
	}
	cmd.SetArgs([]string{"--json", "--human"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected mutually exclusive flag error")
	}
}
