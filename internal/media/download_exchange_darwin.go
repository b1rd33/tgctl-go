//go:build darwin

package media

import "golang.org/x/sys/unix"

func (d *anchoredDir) renameNoReplace(oldName, newName string) error {
	return unix.RenameatxNp(d.fd, oldName, d.fd, newName, unix.RENAME_EXCL)
}

func (d *anchoredDir) exchange(oldName, newName string) error {
	return unix.RenameatxNp(d.fd, oldName, d.fd, newName, unix.RENAME_SWAP)
}
