package media

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	defaultDownloadName = "media.bin"
	maxDownloadNameSize = 180
)

var (
	ErrDestinationExists          = errors.New("download destination already exists")
	ErrUnsafeDestination          = errors.New("download destination is not a regular file")
	ErrDestinationCommitted       = errors.New("download destination is already committed")
	ErrDestinationAborted         = errors.New("download destination is aborted")
	ErrInvalidDestination         = errors.New("invalid download destination")
	ErrDestinationChanged         = errors.New("download destination changed during commit")
	ErrAtomicOverwriteUnsupported = errors.New("atomic safe overwrite is unsupported on this platform")
	ErrLimitExceeded              = errors.New("download size limit exceeded")
	ErrInvalidLimit               = errors.New("download size limit must not be negative")
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
	dir       *anchoredDir
	finalName string
	partName  string
	partID    fileIdentity
	target    targetSnapshot
}

type targetSnapshot struct {
	exists   bool
	identity fileIdentity
}

var (
	generatePartName       = randomPartName
	beforeOverwritePublish = func() {}
)

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
	dirHandle, err := openAnchoredDir(absDir)
	if err != nil {
		return nil, fmt.Errorf("open download directory: %w", err)
	}

	safeName := SanitizeDownloadName(name)
	finalPath := filepath.Join(absDir, safeName)
	if filepath.Dir(finalPath) != absDir {
		_ = dirHandle.close()
		return nil, fmt.Errorf("%w: destination escaped download directory", ErrInvalidDestination)
	}
	target, err := validateFinalTarget(dirHandle, safeName, finalPath, overwrite)
	if err != nil {
		_ = dirHandle.close()
		return nil, err
	}

	partName, part, err := createPartFile(dirHandle, absDir, safeName)
	if err != nil {
		_ = dirHandle.close()
		return nil, fmt.Errorf("create download part: %w", err)
	}
	partEntry, err := snapshotOpenFile(part)
	if err != nil || !partEntry.regular {
		_ = part.Close()
		_ = dirHandle.close()
		if err != nil {
			return nil, fmt.Errorf("inspect opened download part: %w", err)
		}
		return nil, fmt.Errorf("%w: opened part is not regular", ErrUnsafeDestination)
	}
	partPath := filepath.Join(absDir, partName)
	if err := validateNamedRegular(dirHandle, partName, partPath, partEntry.identity); err != nil {
		_ = part.Close()
		_ = dirHandle.close()
		return nil, err
	}
	if err := part.Chmod(0o600); err != nil {
		_ = part.Close()
		_ = removeNamedRegular(dirHandle, partName, partPath, partEntry.identity)
		_ = dirHandle.close()
		return nil, fmt.Errorf("secure download part: %w", err)
	}
	if err := validateNamedRegular(dirHandle, partName, partPath, partEntry.identity); err != nil {
		_ = part.Close()
		_ = dirHandle.close()
		return nil, err
	}
	return &Destination{
		FinalPath: finalPath,
		PartPath:  partPath,
		File:      part,
		overwrite: overwrite,
		state:     destinationOpen,
		finalPath: finalPath,
		partPath:  partPath,
		file:      part,
		dir:       dirHandle,
		finalName: safeName,
		partName:  partName,
		partID:    partEntry.identity,
		target:    target,
	}, nil
}

func validateNamedRegular(dir *anchoredDir, name, displayPath string, expected fileIdentity) error {
	entry, err := dir.lstat(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrDestinationChanged, displayPath)
		}
		return errors.Join(
			fmt.Errorf("%w: %s", ErrDestinationChanged, displayPath),
			fmt.Errorf("inspect destination entry: %w", err),
		)
	}
	if !entry.regular {
		return fmt.Errorf("%w: %s", ErrUnsafeDestination, displayPath)
	}
	if !sameFileIdentity(entry.identity, expected) {
		return fmt.Errorf("%w: %s", ErrDestinationChanged, displayPath)
	}
	return nil
}

func removeNamedRegular(dir *anchoredDir, name, displayPath string, expected fileIdentity) error {
	if err := validateNamedRegular(dir, name, displayPath, expected); err != nil {
		return err
	}
	return dir.remove(name)
}

func createPartFile(dir *anchoredDir, displayDir, safeName string) (string, *os.File, error) {
	for range 100 {
		name, err := generatePartName(safeName)
		if err != nil {
			return "", nil, err
		}
		if filepath.Base(name) != name || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
			return "", nil, fmt.Errorf("%w: invalid generated part name", ErrInvalidDestination)
		}
		file, err := dir.createExclusive(name, filepath.Join(displayDir, name))
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return name, file, err
	}
	return "", nil, fmt.Errorf("could not allocate exclusive part name")
}

func randomPartName(safeName string) (string, error) {
	suffix := make([]byte, 16)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	return "." + safeName + "." + hex.EncodeToString(suffix) + ".part", nil
}

func validateFinalTarget(dir *anchoredDir, name, displayPath string, overwrite bool) (targetSnapshot, error) {
	info, err := dir.lstat(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return targetSnapshot{}, nil
		}
		return targetSnapshot{}, fmt.Errorf("inspect download destination: %w", err)
	}
	if !overwrite {
		return targetSnapshot{}, fmt.Errorf("%w: %s", ErrDestinationExists, displayPath)
	}
	if !info.regular {
		return targetSnapshot{}, fmt.Errorf("%w: %s", ErrUnsafeDestination, displayPath)
	}
	return targetSnapshot{exists: true, identity: info.identity}, nil
}

// Commit syncs and publishes the part file using only the directory handle
// captured by OpenDestination. On Darwin and Linux, replacing an existing
// regular file uses an atomic name exchange; the displaced inode is then
// checked against the inode accepted at open time. A changed, symlink, or
// special target is atomically restored and rejected. Platforms without an
// atomic exchange conservatively reject existing-target overwrites.
func (d *Destination) Commit() error {
	if err := d.lifecycleError(); err != nil {
		return err
	}
	if d.state != destinationOpen || d.file == nil || d.File != d.file || d.dir == nil ||
		d.FinalPath != d.finalPath || d.PartPath != d.partPath ||
		d.FinalPath == "" || d.PartPath == "" || d.finalName == "" || d.partName == "" {
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
		published, publishErr := d.publishOverwrite()
		if publishErr != nil {
			if !published {
				return d.failBeforeCommit(publishErr)
			}
			return errors.Join(publishErr, d.finishCommit())
		}
	} else {
		if err := d.validatePartEntry(); err != nil {
			return d.failBeforeCommit(err)
		}
		if err := d.dir.renameNoReplace(d.partName, d.finalName); err != nil {
			if errors.Is(err, os.ErrExist) {
				err = fmt.Errorf("%w: %s", ErrDestinationExists, d.FinalPath)
			} else {
				err = fmt.Errorf("publish download without overwrite: %w", err)
			}
			return d.failBeforeCommit(err)
		}
		if err := validateNamedRegular(d.dir, d.finalName, d.FinalPath, d.partID); err != nil {
			return errors.Join(
				fmt.Errorf("download published but part identity changed during publish: %w", err),
				d.finishCommit(),
			)
		}
	}

	return d.finishCommit()
}

func (d *Destination) publishOverwrite() (bool, error) {
	if err := d.validateTargetUnchanged(); err != nil {
		return false, err
	}
	if err := d.validatePartEntry(); err != nil {
		return false, err
	}
	beforeOverwritePublish()

	if !d.target.exists {
		if err := d.dir.renameNoReplace(d.partName, d.finalName); err != nil {
			if errors.Is(err, os.ErrExist) {
				return false, d.targetRaceError()
			}
			return false, fmt.Errorf("publish new download: %w", err)
		}
		if err := validateNamedRegular(d.dir, d.finalName, d.FinalPath, d.partID); err != nil {
			return true, fmt.Errorf("download published but part identity changed during publish: %w", err)
		}
		return true, nil
	}

	if err := d.dir.exchange(d.partName, d.finalName); err != nil {
		if errors.Is(err, ErrAtomicOverwriteUnsupported) {
			return false, err
		}
		return false, errors.Join(d.targetRaceError(), fmt.Errorf("atomic exchange download: %w", err))
	}
	finalErr := validateNamedRegular(d.dir, d.finalName, d.FinalPath, d.partID)
	displacedErr := validateNamedRegular(d.dir, d.partName, d.PartPath, d.target.identity)
	if finalErr == nil && displacedErr == nil {
		if err := removeNamedRegular(d.dir, d.partName, d.PartPath, d.target.identity); err != nil {
			return true, fmt.Errorf("download committed but remove displaced target: %w", err)
		}
		return true, nil
	}

	raceErr := errors.Join(finalErr, displacedErr)
	if err := d.dir.exchange(d.partName, d.finalName); err != nil {
		return true, errors.Join(raceErr, fmt.Errorf("unsafe target displaced but rollback failed: %w", err))
	}
	return false, raceErr
}

func (d *Destination) validatePartEntry() error {
	return validateNamedRegular(d.dir, d.partName, d.PartPath, d.partID)
}

func (d *Destination) targetRaceError() error {
	current, err := d.dir.lstat(d.finalName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrDestinationChanged, d.FinalPath)
		}
		return errors.Join(
			fmt.Errorf("%w: %s", ErrDestinationChanged, d.FinalPath),
			fmt.Errorf("inspect changed destination: %w", err),
		)
	}
	if !current.regular {
		return fmt.Errorf("%w: %s", ErrUnsafeDestination, d.FinalPath)
	}
	return fmt.Errorf("%w: %s", ErrDestinationChanged, d.FinalPath)
}

func (d *Destination) validateTargetUnchanged() error {
	current, err := validateFinalTarget(d.dir, d.finalName, d.FinalPath, true)
	if err != nil {
		return err
	}
	if current.exists != d.target.exists ||
		(current.exists && !sameFileIdentity(current.identity, d.target.identity)) {
		return fmt.Errorf("%w: %s", ErrDestinationChanged, d.FinalPath)
	}
	return nil
}

func (d *Destination) finishCommit() error {
	d.state = destinationCommitted
	syncErr := d.dir.sync()
	closeErr := d.closeDir()
	if syncErr != nil {
		syncErr = fmt.Errorf("download committed but sync directory: %w", syncErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("download committed but close directory: %w", closeErr)
	}
	return errors.Join(syncErr, closeErr)
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
	if d.dir != nil && d.partName != "" {
		if err := removeNamedRegular(d.dir, d.partName, d.PartPath, d.partID); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove download part: %w", err))
		}
	}
	if err := d.closeDir(); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("close download directory: %w", err))
	}
	d.state = destinationAborted
	return cleanupErr
}

func (d *Destination) closeDir() error {
	if d.dir == nil {
		return nil
	}
	dir := d.dir
	d.dir = nil
	return dir.close()
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
