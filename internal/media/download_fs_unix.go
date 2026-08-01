//go:build darwin || linux

package media

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

type fileIdentity struct {
	device uint64
	inode  uint64
}

type anchoredEntry struct {
	identity fileIdentity
	regular  bool
}

type anchoredDir struct {
	fd int
}

func openAnchoredDir(path string) (*anchoredDir, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return &anchoredDir{fd: fd}, nil
}

func (d *anchoredDir) createExclusive(name, displayPath string) (*os.File, error) {
	fd, err := unix.Openat(d.fd, name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), displayPath), nil
}

func (d *anchoredDir) lstat(name string) (anchoredEntry, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(d.fd, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return anchoredEntry{}, err
	}
	return anchoredEntry{
		identity: fileIdentity{device: uint64(stat.Dev), inode: stat.Ino},
		regular:  stat.Mode&unix.S_IFMT == unix.S_IFREG,
	}, nil
}

func snapshotOpenFile(file *os.File) (anchoredEntry, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return anchoredEntry{}, err
	}
	return anchoredEntry{
		identity: fileIdentity{device: uint64(stat.Dev), inode: stat.Ino},
		regular:  stat.Mode&unix.S_IFMT == unix.S_IFREG,
	}, nil
}

func sameFileIdentity(a, b fileIdentity) bool {
	return a == b
}

func (d *anchoredDir) remove(name string) error {
	return unix.Unlinkat(d.fd, name, 0)
}

func (d *anchoredDir) sync() error {
	if err := unix.Fsync(d.fd); err != nil && !errors.Is(err, unix.EINVAL) {
		return err
	}
	return nil
}

func (d *anchoredDir) close() error {
	if d == nil || d.fd < 0 {
		return nil
	}
	fd := d.fd
	d.fd = -1
	return unix.Close(fd)
}
