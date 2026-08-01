//go:build linux

package media

import "golang.org/x/sys/unix"

func (d *anchoredDir) renameNoReplace(oldName, newName string) error {
	return unix.Renameat2(d.fd, oldName, d.fd, newName, unix.RENAME_NOREPLACE)
}

func (d *anchoredDir) exchange(oldName, newName string) error {
	return unix.Renameat2(d.fd, oldName, d.fd, newName, unix.RENAME_EXCHANGE)
}
