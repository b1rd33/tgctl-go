//go:build !darwin && !linux

package media

func normalizeAtomicRenameError(err error, _, _ string) error {
	return err
}
