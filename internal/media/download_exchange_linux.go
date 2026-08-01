//go:build linux

package media

import "golang.org/x/sys/unix"

func (d *anchoredDir) renameNoReplace(oldName, newName string) error {
	return normalizeAtomicRenameError(unix.Renameat2(d.fd, oldName, d.fd, newName, unix.RENAME_NOREPLACE), oldName, newName)
}

func (d *anchoredDir) exchange(oldName, newName string) error {
	return normalizeAtomicRenameError(unix.Renameat2(d.fd, oldName, d.fd, newName, unix.RENAME_EXCHANGE), oldName, newName)
}
