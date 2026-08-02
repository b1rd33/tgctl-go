//go:build !darwin && !linux

package media

import "os"

// The secure lifecycle requires atomic exchange and no-replace rename. Rather
// than emulate those with validate-then-act operations, unsupported platforms
// fail closed at OpenDestination.
type fileIdentity struct {
	info os.FileInfo
}

type anchoredEntry struct {
	identity fileIdentity
	regular  bool
	size     int64
}

type anchoredDir struct{}

func openAnchoredDir(string) (*anchoredDir, error) {
	return nil, ErrAtomicOverwriteUnsupported
}

func (d *anchoredDir) createExclusive(string, string) (*os.File, error) {
	return nil, ErrAtomicOverwriteUnsupported
}

func (d *anchoredDir) lstat(string) (anchoredEntry, error) {
	return anchoredEntry{}, ErrAtomicOverwriteUnsupported
}

func (d *anchoredDir) identity() (fileIdentity, error) {
	return fileIdentity{}, ErrAtomicOverwriteUnsupported
}

func snapshotOpenFile(file *os.File) (anchoredEntry, error) {
	info, err := file.Stat()
	if err != nil {
		return anchoredEntry{}, err
	}
	return anchoredEntry{identity: fileIdentity{info: info}, regular: info.Mode().IsRegular(), size: info.Size()}, nil
}

func sameFileIdentity(a, b fileIdentity) bool {
	return a.info != nil && b.info != nil && os.SameFile(a.info, b.info)
}

func sameStrictFileIdentity(a, b fileIdentity) bool {
	return sameFileIdentity(a, b)
}

func (d *anchoredDir) renameNoReplace(string, string) error {
	return ErrAtomicOverwriteUnsupported
}

func (d *anchoredDir) exchange(string, string) error {
	return ErrAtomicOverwriteUnsupported
}

func (d *anchoredDir) remove(string) error {
	return ErrAtomicOverwriteUnsupported
}

func (d *anchoredDir) sync() error {
	return ErrAtomicOverwriteUnsupported
}

func (d *anchoredDir) close() error {
	return nil
}
