package safety

import (
	"context"
	"path/filepath"
)

type fileDigestKey struct{}

func WithFileDigests(ctx context.Context, values map[string]string) context.Context {
	copy := map[string]string{}
	for path, digest := range values {
		abs, err := filepath.Abs(path)
		if err == nil {
			copy[abs] = digest
		}
	}
	return context.WithValue(ctx, fileDigestKey{}, copy)
}
func ExpectedFileDigest(ctx context.Context, path string) string {
	values, _ := ctx.Value(fileDigestKey{}).(map[string]string)
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	return values[abs]
}
