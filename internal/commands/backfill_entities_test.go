package commands

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/safety"
)

func TestRunBackfillEntitiesRejectsNumericBoundsBeforeArtifacts(t *testing.T) {
	if strconv.IntSize == 32 {
		t.Skip("native int cannot represent values above int32")
	}
	root := t.TempDir()
	over := int64(math.MaxInt32)
	over++
	_, err := runBackfillEntities(context.Background(), 1, "hash", filepath.Join(root, "session", "tg.session"), filepath.Join(root, "db", "telegram.sqlite"), int(over))
	var badArgs *safety.BadArgs
	if !errors.As(err, &badArgs) {
		t.Fatalf("limit error=%v, want BadArgs", err)
	}
	_, err = runBackfillEntities(context.Background(), int(over), "hash", filepath.Join(root, "session", "tg.session"), filepath.Join(root, "db", "telegram.sqlite"), 1)
	var missing *safety.MissingCredentials
	if !errors.As(err, &missing) {
		t.Fatalf("API ID error=%v, want MissingCredentials", err)
	}
}
