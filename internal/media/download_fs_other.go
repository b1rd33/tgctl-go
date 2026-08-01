//go:build !darwin && !linux

package media

import (
	"errors"
	"os"
)

type fileIdentity struct {
	info os.FileInfo
}

type anchoredEntry struct {
	identity fileIdentity
	regular  bool
}

type anchoredDir struct {
	root     *os.Root
	syncFile *os.File
}

func openAnchoredDir(path string) (*anchoredDir, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	syncFile, err := root.Open(".")
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	return &anchoredDir{root: root, syncFile: syncFile}, nil
}

func (d *anchoredDir) createExclusive(name, _ string) (*os.File, error) {
	return d.root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
}

func (d *anchoredDir) lstat(name string) (anchoredEntry, error) {
	info, err := d.root.Lstat(name)
	if err != nil {
		return anchoredEntry{}, err
	}
	return anchoredEntry{identity: fileIdentity{info: info}, regular: info.Mode().IsRegular()}, nil
}

func snapshotOpenFile(file *os.File) (anchoredEntry, error) {
	info, err := file.Stat()
	if err != nil {
		return anchoredEntry{}, err
	}
	return anchoredEntry{identity: fileIdentity{info: info}, regular: info.Mode().IsRegular()}, nil
}

func sameFileIdentity(a, b fileIdentity) bool {
	return a.info != nil && b.info != nil && os.SameFile(a.info, b.info)
}

func (d *anchoredDir) renameNoReplace(oldName, newName string) error {
	if err := d.root.Link(oldName, newName); err != nil {
		return err
	}
	return d.root.Remove(oldName)
}

func (d *anchoredDir) exchange(_, _ string) error {
	return ErrAtomicOverwriteUnsupported
}

func (d *anchoredDir) remove(name string) error {
	return d.root.Remove(name)
}

func (d *anchoredDir) sync() error {
	// Directory sync is not supported consistently on fallback platforms.
	_ = d.syncFile.Sync()
	return nil
}

func (d *anchoredDir) close() error {
	if d == nil || d.root == nil {
		return nil
	}
	syncErr := d.syncFile.Close()
	rootErr := d.root.Close()
	d.syncFile = nil
	d.root = nil
	return errors.Join(syncErr, rootErr)
}
