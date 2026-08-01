//go:build darwin

package media

import "golang.org/x/sys/unix"

func (d *anchoredDir) renameNoReplace(oldName, newName string) error {
	return normalizeAtomicRenameError(unix.RenameatxNp(d.fd, oldName, d.fd, newName, unix.RENAME_EXCL), oldName, newName)
}

func (d *anchoredDir) exchange(oldName, newName string) error {
	return normalizeAtomicRenameError(unix.RenameatxNp(d.fd, oldName, d.fd, newName, unix.RENAME_SWAP), oldName, newName)
}
