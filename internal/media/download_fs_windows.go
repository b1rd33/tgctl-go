//go:build windows

package media

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// Windows does not expose the Unix *at family through Go's portable syscall
// layer. Keep the directory handle and resolve only validated single
// components against that opened directory. All public operations re-check
// the entry identity before acting, and MoveFileEx refuses replacement for
// no-replace publication on Windows.
type fileIdentity struct{ info os.FileInfo }

type anchoredEntry struct {
	identity fileIdentity
	regular  bool
	size     int64
}

type anchoredDir struct {
	path string
	file *os.File
}

func openAnchoredDir(path string) (*anchoredDir, error) {
	file, err := openWindowsPath(path, windows.GENERIC_READ, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, windows.OPEN_EXISTING)
	if err != nil {
		return nil, err
	}
	return &anchoredDir{path: path, file: file}, nil
}

func (d *anchoredDir) entryPath(name string) string { return filepath.Join(d.path, name) }

func (d *anchoredDir) createExclusive(name, displayPath string) (*os.File, error) {
	return openWindowsPath(d.entryPath(name), windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_ATTRIBUTE_NORMAL, windows.CREATE_NEW)
}

func (d *anchoredDir) open(name, displayPath string) (*os.File, error) {
	return openWindowsPath(d.entryPath(name), windows.GENERIC_READ, windows.FILE_FLAG_OPEN_REPARSE_POINT, windows.OPEN_EXISTING)
}

func openWindowsPath(path string, access, attrs, disposition uint32) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(
		p, access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, disposition, attrs, 0,
	)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

func (d *anchoredDir) lstat(name string) (anchoredEntry, error) {
	info, err := os.Lstat(d.entryPath(name))
	if err != nil {
		return anchoredEntry{}, err
	}
	return anchoredEntry{identity: fileIdentity{info: info}, regular: info.Mode().IsRegular(), size: info.Size()}, nil
}

func (d *anchoredDir) identity() (fileIdentity, error) {
	info, err := d.file.Stat()
	if err != nil {
		return fileIdentity{}, err
	}
	return fileIdentity{info: info}, nil
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

func sameStrictFileIdentity(a, b fileIdentity) bool { return sameFileIdentity(a, b) }

func (d *anchoredDir) renameNoReplace(oldName, newName string) error {
	if _, err := os.Lstat(d.entryPath(newName)); err == nil {
		return fs.ErrExist
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.Rename(d.entryPath(oldName), d.entryPath(newName))
}

func (d *anchoredDir) exchange(oldName, newName string) error {
	// Windows has no portable atomic exchange in the standard Go API. Use an
	// unpredictable same-directory staging name and verify each move. The
	// caller still validates both identities and rolls back on any mismatch.
	var staging string
	for range 100 {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			return err
		}
		staging = ".tgctl-exchange-" + hex.EncodeToString(buf)
		if _, err := os.Lstat(d.entryPath(staging)); errors.Is(err, fs.ErrNotExist) {
			break
		}
		staging = ""
	}
	if staging == "" {
		return errors.Join(ErrAtomicOverwriteUnsupported, errors.New("could not allocate exchange staging name"))
	}
	stagePath := d.entryPath(staging)
	oldPath := d.entryPath(oldName)
	newPath := d.entryPath(newName)
	if err := os.Rename(newPath, stagePath); err != nil {
		return err
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		_ = os.Rename(stagePath, newPath)
		return err
	}
	if err := os.Rename(stagePath, oldPath); err != nil {
		// Best-effort rollback; the caller will detect identity loss and report
		// cleanup incomplete if the public state cannot be restored.
		_ = os.Rename(newPath, oldPath)
		_ = os.Rename(stagePath, newPath)
		return err
	}
	return nil
}

func (d *anchoredDir) remove(name string) error { return os.Remove(d.entryPath(name)) }

// Directory handles cannot be flushed portably on Windows. File.Sync and the
// publication MoveFileEx write-through flag provide the available durability.
func (d *anchoredDir) sync() error { return nil }

func (d *anchoredDir) close() error {
	if d == nil || d.file == nil {
		return nil
	}
	err := d.file.Close()
	d.file = nil
	return err
}
