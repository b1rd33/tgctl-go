package media

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

const (
	defaultDownloadName = "media.bin"
	maxDownloadNameSize = 180
)

var (
	ErrDestinationExists    = errors.New("download destination already exists")
	ErrUnsafeDestination    = errors.New("download destination is not a regular file")
	ErrDestinationCommitted = errors.New("download destination is already committed")
	ErrDestinationAborted   = errors.New("download destination is aborted")
	ErrInvalidDestination   = errors.New("invalid download destination")
	ErrLimitExceeded        = errors.New("download size limit exceeded")
	ErrInvalidLimit         = errors.New("download size limit must not be negative")
)

type destinationState uint8

const (
	destinationOpen destinationState = iota + 1
	destinationCommitted
	destinationAborted
)

// Destination holds an open temporary download and its eventual final path.
type Destination struct {
	FinalPath string
	PartPath  string
	File      *os.File

	overwrite bool
	state     destinationState
	finalPath string
	partPath  string
	file      *os.File
}

// SanitizeDownloadName reduces name to one safe, portable path component.
func SanitizeDownloadName(name string) string {
	name = strings.ToValidUTF8(name, "_")
	// filepath.Base only recognizes the host separator. Normalize the other
	// commonly supplied separator first so Windows-originated names are safe on
	// Unix too.
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))

	var clean strings.Builder
	clean.Grow(len(name))
	for _, r := range name {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			clean.WriteByte('_')
			continue
		}
		clean.WriteRune(r)
	}
	name = strings.Trim(clean.String(), ". ")
	if unusableDownloadName(name) {
		return defaultDownloadName
	}
	if len(name) <= maxDownloadNameSize {
		return name
	}

	ext := filepath.Ext(name)
	if ext != "" && ext != "." && len(ext) <= 32 {
		stem := strings.Trim(strings.TrimSuffix(name, ext), ". ")
		stem = strings.Trim(truncateUTF8(stem, maxDownloadNameSize-len(ext)), ". ")
		if stem != "" {
			return stem + ext
		}
	}
	name = strings.Trim(truncateUTF8(name, maxDownloadNameSize), ". ")
	if unusableDownloadName(name) {
		return defaultDownloadName
	}
	return name
}

func unusableDownloadName(name string) bool {
	return name == "" || name == "." || name == ".."
}

func truncateUTF8(value string, max int) string {
	if len(value) <= max {
		return value
	}
	value = value[:max]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

// OpenDestination creates a private, exclusive part file in dir. With
// overwrite disabled, an existing final name is reported deterministically.
func OpenDestination(dir, name string, overwrite bool) (*Destination, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve download directory: %w", err)
	}
	dirExisted := true
	if _, err := os.Stat(absDir); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect download directory: %w", err)
		}
		dirExisted = false
	}
	if err := os.MkdirAll(absDir, 0o700); err != nil {
		return nil, fmt.Errorf("create download directory: %w", err)
	}
	if !dirExisted {
		if err := os.Chmod(absDir, 0o700); err != nil {
			return nil, fmt.Errorf("secure download directory: %w", err)
		}
	}
	dirInfo, err := os.Stat(absDir)
	if err != nil {
		return nil, fmt.Errorf("inspect download directory: %w", err)
	}
	if !dirInfo.IsDir() {
		return nil, fmt.Errorf("%w: download directory is not a directory: %s", ErrUnsafeDestination, absDir)
	}

	safeName := SanitizeDownloadName(name)
	finalPath := filepath.Join(absDir, safeName)
	if filepath.Dir(finalPath) != absDir {
		return nil, fmt.Errorf("%w: destination escaped download directory", ErrInvalidDestination)
	}
	if err := validateFinalTarget(finalPath, overwrite); err != nil {
		return nil, err
	}

	part, err := os.CreateTemp(absDir, "."+safeName+".*.part")
	if err != nil {
		return nil, fmt.Errorf("create download part: %w", err)
	}
	if err := part.Chmod(0o600); err != nil {
		_ = part.Close()
		_ = os.Remove(part.Name())
		return nil, fmt.Errorf("secure download part: %w", err)
	}
	return &Destination{
		FinalPath: finalPath,
		PartPath:  part.Name(),
		File:      part,
		overwrite: overwrite,
		state:     destinationOpen,
		finalPath: finalPath,
		partPath:  part.Name(),
		file:      part,
	}, nil
}

func validateFinalTarget(path string, overwrite bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect download destination: %w", err)
	}
	if !overwrite {
		return fmt.Errorf("%w: %s", ErrDestinationExists, path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s", ErrUnsafeDestination, path)
	}
	return nil
}

// Commit syncs and publishes the part file. A non-overwriting commit uses an
// atomic hard-link publication so a concurrently created final is not clobbered.
func (d *Destination) Commit() error {
	if err := d.lifecycleError(); err != nil {
		return err
	}
	if d.state != destinationOpen || d.file == nil || d.File != d.file ||
		d.FinalPath != d.finalPath || d.PartPath != d.partPath ||
		d.FinalPath == "" || d.PartPath == "" {
		if d.state == destinationOpen {
			return d.failBeforeCommit(ErrInvalidDestination)
		}
		return ErrInvalidDestination
	}
	if filepath.Dir(d.FinalPath) != filepath.Dir(d.PartPath) {
		return d.failBeforeCommit(fmt.Errorf("%w: final and part directories differ", ErrInvalidDestination))
	}
	if err := d.file.Chmod(0o600); err != nil {
		return d.failBeforeCommit(fmt.Errorf("secure download: %w", err))
	}
	if err := d.file.Sync(); err != nil {
		return d.failBeforeCommit(fmt.Errorf("sync download: %w", err))
	}
	if err := d.file.Close(); err != nil {
		d.file = nil
		d.File = nil
		return d.failBeforeCommit(fmt.Errorf("close download: %w", err))
	}
	d.file = nil
	d.File = nil

	if d.overwrite {
		if err := validateFinalTarget(d.FinalPath, true); err != nil {
			return d.failBeforeCommit(err)
		}
		if err := os.Rename(d.PartPath, d.FinalPath); err != nil {
			return d.failBeforeCommit(fmt.Errorf("publish download: %w", err))
		}
	} else {
		if err := os.Link(d.PartPath, d.FinalPath); err != nil {
			if errors.Is(err, os.ErrExist) {
				err = fmt.Errorf("%w: %s", ErrDestinationExists, d.FinalPath)
			} else {
				err = fmt.Errorf("publish download without overwrite: %w", err)
			}
			return d.failBeforeCommit(err)
		}
		if err := os.Remove(d.PartPath); err != nil {
			d.state = destinationCommitted
			return fmt.Errorf("download committed but remove part: %w", err)
		}
	}

	d.state = destinationCommitted
	if err := syncDirectory(filepath.Dir(d.FinalPath)); err != nil {
		return fmt.Errorf("download committed but sync directory: %w", err)
	}
	return nil
}

func (d *Destination) lifecycleError() error {
	if d == nil {
		return ErrInvalidDestination
	}
	switch d.state {
	case destinationCommitted:
		return ErrDestinationCommitted
	case destinationAborted:
		return ErrDestinationAborted
	default:
		return nil
	}
}

func (d *Destination) failBeforeCommit(primary error) error {
	cleanupErr := d.abortOpen()
	return errors.Join(primary, cleanupErr)
}

// Abort closes the part file and removes it. Repeated aborts are harmless.
func (d *Destination) Abort() error {
	if d == nil {
		return ErrInvalidDestination
	}
	switch d.state {
	case destinationCommitted:
		return ErrDestinationCommitted
	case destinationAborted:
		return nil
	case destinationOpen:
		return d.abortOpen()
	default:
		return ErrInvalidDestination
	}
}

func (d *Destination) abortOpen() error {
	var cleanupErr error
	if d.file != nil {
		originalFile := d.file
		if err := originalFile.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("close download part: %w", err))
		}
		d.file = nil
		if d.File == originalFile {
			d.File = nil
		}
	}
	if d.partPath != "" {
		if err := os.Remove(d.partPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove download part: %w", err))
		}
	}
	d.state = destinationAborted
	return cleanupErr
}

func syncDirectory(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if errors.Is(syncErr, syscall.EINVAL) {
		syncErr = nil
	}
	return errors.Join(syncErr, closeErr)
}

// LimitWriter writes at most Max bytes. Max == 0 is unlimited; Max < 0 is
// invalid. N tracks bytes reported written by the underlying writer.
type LimitWriter struct {
	W   io.Writer
	N   int64
	Max int64
}

func (w *LimitWriter) Write(p []byte) (int, error) {
	if w == nil || w.W == nil {
		return 0, io.ErrClosedPipe
	}
	if w.Max < 0 || w.N < 0 {
		return 0, ErrInvalidLimit
	}

	allowed := len(p)
	limited := false
	if w.Max > 0 {
		remaining := w.Max - w.N
		if remaining <= 0 && len(p) != 0 {
			return 0, ErrLimitExceeded
		}
		if int64(allowed) > remaining {
			allowed = int(remaining)
			limited = true
		}
	}

	n, err := w.W.Write(p[:allowed])
	if n < 0 || n > allowed {
		return 0, fmt.Errorf("invalid underlying write count %d for %d bytes", n, allowed)
	}
	w.N += int64(n)
	if err != nil {
		if limited && n == allowed {
			return n, errors.Join(err, ErrLimitExceeded)
		}
		return n, err
	}
	if n != allowed {
		return n, io.ErrShortWrite
	}
	if limited {
		return n, ErrLimitExceeded
	}
	return n, nil
}
