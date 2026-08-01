//go:build darwin || linux

package media

import (
	"errors"

	"golang.org/x/sys/unix"
)

func normalizeAtomicRenameError(err error, oldName, newName string) error {
	if err == nil || errors.Is(err, ErrAtomicOverwriteUnsupported) {
		return err
	}
	unsupported := errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOTSUP)
	// These wrappers always use anchored descriptors, known flags, and validated
	// path components. Under those preconditions EINVAL means the filesystem or
	// kernel rejected the requested atomic rename semantic, not bad arguments.
	if errors.Is(err, unix.EINVAL) && validAtomicRenameComponent(oldName) && validAtomicRenameComponent(newName) {
		unsupported = true
	}
	if unsupported {
		return errors.Join(ErrAtomicOverwriteUnsupported, err)
	}
	return err
}
