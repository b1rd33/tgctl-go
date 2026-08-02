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
	"sync"
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
	ErrAtomicOverwriteUnsupported = errors.New("atomic safe overwrite is unsupported on this platform or filesystem")
	ErrCleanupIncomplete          = errors.New("download cleanup incomplete")
	ErrLimitExceeded              = errors.New("download size limit exceeded")
	ErrInvalidLimit               = errors.New("download size limit must not be negative")
)

// DestinationExistsError reports a no-overwrite collision that was proven to
// be a regular file using the destination's anchored directory descriptor.
// FinalPath, Size, and Identity are captured from that same no-follow
// inspection. Callers must not restat FinalPath to classify the collision.
type DestinationExistsError struct {
	FinalPath string
	Size      int64
	Identity  ArtifactIdentity
}

func (e *DestinationExistsError) Error() string {
	if e == nil {
		return ErrDestinationExists.Error()
	}
	return fmt.Sprintf("%s: %s", ErrDestinationExists, e.FinalPath)
}

func (e *DestinationExistsError) Unwrap() error { return ErrDestinationExists }

type destinationState uint8

const (
	destinationOpen destinationState = iota + 1
	destinationCommitted
	destinationAborted
	destinationCleanupIncomplete
)

// Destination holds an open temporary download and its eventual final path.
// Commit and Abort are safe to call concurrently with each other after writes
// to File have stopped. The exported fields must not be mutated concurrently.
type Destination struct {
	FinalPath string
	PartPath  string
	File      *os.File

	overwrite        bool
	state            destinationState
	published        bool
	finalPath        string
	partPath         string
	file             *os.File
	dir              *anchoredDir
	finalName        string
	partName         string
	dirID            fileIdentity
	partID           fileIdentity
	publishedID      fileIdentity
	publishedIDValid bool
	target           targetSnapshot

	mu          sync.Mutex
	ops         destinationOps
	terminalErr error
}

type targetSnapshot struct {
	exists   bool
	identity fileIdentity
}

type destinationOps struct {
	createExclusive        func(*anchoredDir, string, string) (*os.File, error)
	renameNoReplace        func(*anchoredDir, string, string) error
	exchange               func(*anchoredDir, string, string) error
	remove                 func(*anchoredDir, string) error
	syncDir                func(*anchoredDir) error
	closeDir               func(*anchoredDir) error
	syncFile               func(*os.File) error
	closeFile              func(*os.File) error
	generatePartName       func(string) (string, error)
	randomPrivateName      func(string) (string, error)
	beforeOverwrite        func()
	beforeNoReplace        func()
	beforeAbsentPublish    func()
	beforeQuarantineDelete func()
	beforeProbeDelete      func(string)
}

func defaultDestinationOps() destinationOps {
	return destinationOps{
		createExclusive:        func(dir *anchoredDir, name, path string) (*os.File, error) { return dir.createExclusive(name, path) },
		renameNoReplace:        func(dir *anchoredDir, oldName, newName string) error { return dir.renameNoReplace(oldName, newName) },
		exchange:               func(dir *anchoredDir, oldName, newName string) error { return dir.exchange(oldName, newName) },
		remove:                 func(dir *anchoredDir, name string) error { return dir.remove(name) },
		syncDir:                func(dir *anchoredDir) error { return dir.sync() },
		closeDir:               func(dir *anchoredDir) error { return dir.close() },
		syncFile:               func(file *os.File) error { return file.Sync() },
		closeFile:              func(file *os.File) error { return file.Close() },
		generatePartName:       randomPartName,
		randomPrivateName:      randomPrivateName,
		beforeOverwrite:        func() {},
		beforeNoReplace:        func() {},
		beforeAbsentPublish:    func() {},
		beforeQuarantineDelete: func() {},
		beforeProbeDelete:      func(string) {},
	}
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
		if strings.ContainsRune(`<>:"/\|?*`, r) || unicode.IsControl(r) {
			clean.WriteByte('_')
			continue
		}
		clean.WriteRune(r)
	}
	name = strings.Trim(clean.String(), ". ")
	if unusableDownloadName(name) {
		return defaultDownloadName
	}
	name = truncateDownloadName(name, maxDownloadNameSize)
	if unusableDownloadName(name) {
		return defaultDownloadName
	}
	if reservedDeviceName(name) {
		name = "_" + truncateDownloadName(name, maxDownloadNameSize-1)
	}
	return name
}

func truncateDownloadName(name string, max int) string {
	if len(name) <= max {
		return name
	}
	ext := filepath.Ext(name)
	if ext != "" && ext != "." && len(ext) <= 32 {
		stem := strings.Trim(strings.TrimSuffix(name, ext), ". ")
		stem = strings.Trim(truncateUTF8(stem, max-len(ext)), ". ")
		if stem != "" {
			return stem + ext
		}
	}
	return strings.Trim(truncateUTF8(name, max), ". ")
}

func reservedDeviceName(name string) bool {
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	upper := strings.ToUpper(stem)
	reserved := upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL"
	if len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) {
		reserved = upper[3] >= '1' && upper[3] <= '9'
	}
	return reserved
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
	return openDestinationWithOps(dir, name, overwrite, defaultDestinationOps())
}

func openDestinationWithOps(dir, name string, overwrite bool, ops destinationOps) (*Destination, error) {
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
	dirID, err := dirHandle.identity()
	if err != nil {
		return nil, closeOpenDestinationDir(ops, dirHandle, fmt.Errorf("inspect download directory identity: %w", err))
	}

	safeName := SanitizeDownloadName(name)
	finalPath := filepath.Join(absDir, safeName)
	if filepath.Dir(finalPath) != absDir {
		return nil, closeOpenDestinationDir(ops, dirHandle, fmt.Errorf("%w: destination escaped download directory", ErrInvalidDestination))
	}
	target, err := validateFinalTarget(dirHandle, safeName, finalPath, overwrite)
	if err != nil {
		return nil, closeOpenDestinationDir(ops, dirHandle, err)
	}
	if err := probeAtomicCapabilities(ops, dirHandle, absDir, overwrite && target.exists); err != nil {
		return nil, closeOpenDestinationDir(ops, dirHandle, err)
	}

	partName, part, err := createPartFile(ops, dirHandle, absDir, safeName)
	if err != nil {
		return nil, closeOpenDestinationDir(ops, dirHandle, fmt.Errorf("create download part: %w", err))
	}
	partEntry, err := snapshotOpenFile(part)
	if err != nil || !partEntry.regular {
		var primary error
		if err != nil {
			primary = fmt.Errorf("inspect opened download part: %w", err)
		} else {
			primary = fmt.Errorf("%w: opened part is not regular", ErrUnsafeDestination)
		}
		return nil, cleanupOpenDestinationPart(ops, dirHandle, part, partName, filepath.Join(absDir, partName), partEntry.identity, false, primary)
	}
	partPath := filepath.Join(absDir, partName)
	if err := validateNamedRegular(dirHandle, partName, partPath, partEntry.identity); err != nil {
		return nil, cleanupOpenDestinationPart(ops, dirHandle, part, partName, partPath, partEntry.identity, true, err)
	}
	if err := part.Chmod(0o600); err != nil {
		return nil, cleanupOpenDestinationPart(ops, dirHandle, part, partName, partPath, partEntry.identity, true, fmt.Errorf("secure download part: %w", err))
	}
	if err := validateNamedRegular(dirHandle, partName, partPath, partEntry.identity); err != nil {
		return nil, cleanupOpenDestinationPart(ops, dirHandle, part, partName, partPath, partEntry.identity, true, err)
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
		dirID:     dirID,
		partID:    partEntry.identity,
		target:    target,
		ops:       ops,
	}, nil
}

func closeOpenDestinationDir(ops destinationOps, dir *anchoredDir, primary error) error {
	if err := ops.closeDir(dir); err != nil {
		return errors.Join(primary, ErrCleanupIncomplete, fmt.Errorf("close download directory: %w", err))
	}
	return primary
}

func cleanupOpenDestinationPart(
	ops destinationOps,
	dir *anchoredDir,
	part *os.File,
	partName, partPath string,
	partID fileIdentity,
	canRemove bool,
	primary error,
) error {
	var cleanupErr error
	if err := ops.closeFile(part); err != nil {
		cleanupErr = errors.Join(cleanupErr, ErrCleanupIncomplete, fmt.Errorf("close download part: %w", err))
	}
	if canRemove {
		if err := quarantineRemove(ops, dir, partName, partPath, partID); err != nil {
			cleanupErr = errors.Join(cleanupErr, ErrCleanupIncomplete, fmt.Errorf("remove download part: %w", err))
		}
	} else {
		cleanupErr = errors.Join(cleanupErr, ErrCleanupIncomplete, errors.New("opened download part identity could not be safely removed"))
	}
	return closeOpenDestinationDir(ops, dir, errors.Join(primary, cleanupErr))
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

func validAtomicRenameComponent(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}

type atomicProbeEntry struct {
	name     string
	identity fileIdentity
}

func probeAtomicCapabilities(ops destinationOps, dir *anchoredDir, displayDir string, needExchange bool) error {
	entries := make([]atomicProbeEntry, 0, 2)
	cleanup := func(primary error) error {
		return errors.Join(primary, cleanupAtomicProbeEntries(ops, dir, displayDir, entries))
	}

	firstName, firstID, err := createAtomicProbeEntry(ops, dir, displayDir)
	if firstName != "" {
		entries = append(entries, atomicProbeEntry{name: firstName, identity: firstID})
	}
	if err != nil {
		return cleanup(err)
	}
	secondName, secondID, err := createAtomicProbeEntry(ops, dir, displayDir)
	if secondName != "" {
		entries = append(entries, atomicProbeEntry{name: secondName, identity: secondID})
	}
	if err != nil {
		return cleanup(err)
	}

	renameErr := normalizeAtomicRenameError(ops.renameNoReplace(dir, firstName, secondName), firstName, secondName)
	if !errors.Is(renameErr, os.ErrExist) {
		if renameErr == nil {
			renameErr = errors.Join(ErrAtomicOverwriteUnsupported, errors.New("no-replace rename replaced an existing probe entry"))
			entries = []atomicProbeEntry{{name: secondName, identity: firstID}}
		}
		return cleanup(renameErr)
	}
	if !needExchange {
		return cleanup(nil)
	}

	exchangeErr := normalizeAtomicRenameError(ops.exchange(dir, firstName, secondName), firstName, secondName)
	if exchangeErr != nil {
		return cleanup(exchangeErr)
	}
	// A successful rename updates ctime on Unix. Refresh the identities after
	// the probe operation so strict cleanup still detects later replacements
	// without mistaking the probe's own rename for an attacker mutation.
	firstCurrent, firstInspectErr := dir.lstat(firstName)
	secondCurrent, secondInspectErr := dir.lstat(secondName)
	if firstInspectErr != nil || secondInspectErr != nil {
		return cleanup(errors.Join(firstInspectErr, secondInspectErr, ErrAtomicOverwriteUnsupported))
	}
	entries[0].identity, entries[1].identity = firstCurrent.identity, secondCurrent.identity
	firstErr := validateNamedRegular(dir, entries[0].name, filepath.Join(displayDir, entries[0].name), entries[0].identity)
	secondErr := validateNamedRegular(dir, entries[1].name, filepath.Join(displayDir, entries[1].name), entries[1].identity)
	if firstErr != nil || secondErr != nil {
		return cleanup(errors.Join(
			ErrAtomicOverwriteUnsupported,
			errors.New("atomic exchange probe did not swap both entries"),
			firstErr,
			secondErr,
		))
	}
	return cleanup(nil)
}

func cleanupAtomicProbeEntries(ops destinationOps, dir *anchoredDir, displayDir string, entries []atomicProbeEntry) error {
	var cleanupErr error
	for _, entry := range entries {
		displayPath := filepath.Join(displayDir, entry.name)
		if err := validateNamedRegularStrict(dir, entry.name, displayPath, entry.identity); err != nil {
			cleanupErr = errors.Join(cleanupErr, ErrCleanupIncomplete, fmt.Errorf("validate atomic capability probe before cleanup: %w", err))
			continue
		}
		ops.beforeProbeDelete(entry.name)
		if err := deletePrivateRegular(ops, dir, entry.name, displayPath, entry.identity); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove atomic capability probe: %w", err))
		}
	}
	return cleanupErr
}

func validateNamedRegularStrict(dir *anchoredDir, name, displayPath string, expected fileIdentity) error {
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
	if !sameStrictFileIdentity(entry.identity, expected) {
		return fmt.Errorf("%w: %s", ErrDestinationChanged, displayPath)
	}
	return nil
}

func createAtomicProbeEntry(ops destinationOps, dir *anchoredDir, displayDir string) (string, fileIdentity, error) {
	for range 100 {
		name, err := ops.randomPrivateName(".tgctl-probe-")
		if err != nil {
			return "", fileIdentity{}, fmt.Errorf("generate atomic capability probe name: %w", err)
		}
		if !validAtomicRenameComponent(name) {
			return "", fileIdentity{}, errors.Join(ErrInvalidDestination, errors.New("invalid atomic capability probe name"))
		}
		file, err := ops.createExclusive(dir, name, filepath.Join(displayDir, name))
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", fileIdentity{}, fmt.Errorf("create atomic capability probe: %w", err)
		}
		entry, inspectErr := snapshotOpenFile(file)
		closeErr := ops.closeFile(file)
		if inspectErr != nil || closeErr != nil || !entry.regular {
			primary := inspectErr
			if closeErr != nil {
				primary = errors.Join(primary, ErrCleanupIncomplete, fmt.Errorf("close atomic capability probe: %w", closeErr))
			}
			if !entry.regular && inspectErr == nil {
				primary = errors.Join(primary, ErrUnsafeDestination, errors.New("atomic capability probe is not regular"))
			}
			return name, entry.identity, primary
		}
		return name, entry.identity, nil
	}
	return "", fileIdentity{}, errors.Join(
		ErrAtomicOverwriteUnsupported,
		errors.New("could not allocate atomic capability probe name after 100 attempts"),
	)
}

// quarantineRemove atomically moves a public entry to an unpredictable private
// name before inspecting and deleting it. The supported attacker model permits
// same-user mutation of public part/final names, but assumes the 128-bit
// quarantine name is not discovered and targeted during this short operation.
// Unix platforms use descriptor-anchored rename primitives. Windows uses
// handle/path operations with the same exclusive and identity checks; where
// the platform cannot provide an operation safely, the lifecycle fails closed.
func quarantineRemove(ops destinationOps, dir *anchoredDir, name, displayPath string, expected fileIdentity) error {
	quarantineName, err := captureNamedRegular(ops, dir, name, displayPath, expected)
	if err != nil {
		return err
	}
	return deletePrivateRegular(ops, dir, quarantineName, displayPath, expected)
}

func captureNamedRegular(ops destinationOps, dir *anchoredDir, name, displayPath string, expected fileIdentity) (string, error) {
	for range 100 {
		quarantineName, err := ops.randomPrivateName(".tgctl-quarantine-")
		if err != nil {
			return "", err
		}
		if err := normalizeAtomicRenameError(ops.renameNoReplace(dir, name, quarantineName), name, quarantineName); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			if errors.Is(err, ErrAtomicOverwriteUnsupported) {
				return "", fmt.Errorf("quarantine destination entry: %w", err)
			}
			if errors.Is(err, os.ErrNotExist) {
				return "", fmt.Errorf("%w: %s", ErrDestinationChanged, displayPath)
			}
			return "", errors.Join(
				fmt.Errorf("%w: %s", ErrDestinationChanged, displayPath),
				fmt.Errorf("quarantine destination entry: %w", err),
			)
		}

		identityErr := validateNamedRegular(dir, quarantineName, displayPath, expected)
		if identityErr != nil {
			if err := normalizeAtomicRenameError(ops.renameNoReplace(dir, quarantineName, name), quarantineName, name); err != nil {
				return "", errors.Join(
					identityErr,
					ErrCleanupIncomplete,
					fmt.Errorf("restore quarantined destination entry: %w", err),
				)
			}
			return "", identityErr
		}
		return quarantineName, nil
	}
	return "", errors.Join(ErrCleanupIncomplete, errors.New("could not allocate quarantine name"))
}

func restorePrivateRegular(ops destinationOps, dir *anchoredDir, privateName, publicName, displayPath string, expected fileIdentity) error {
	if err := validateNamedRegular(dir, privateName, displayPath, expected); err != nil {
		return errors.Join(ErrCleanupIncomplete, err)
	}
	if err := normalizeAtomicRenameError(ops.renameNoReplace(dir, privateName, publicName), privateName, publicName); err != nil {
		return errors.Join(
			ErrCleanupIncomplete,
			fmt.Errorf("restore private destination entry: %w", err),
		)
	}
	if err := validateNamedRegular(dir, publicName, displayPath, expected); err != nil {
		return errors.Join(ErrCleanupIncomplete, err)
	}
	return nil
}

func deletePrivateRegular(ops destinationOps, dir *anchoredDir, privateName, displayPath string, expected fileIdentity) error {
	if err := validateNamedRegular(dir, privateName, displayPath, expected); err != nil {
		return errors.Join(ErrCleanupIncomplete, err)
	}
	ops.beforeQuarantineDelete()
	if err := ops.remove(dir, privateName); err != nil {
		return errors.Join(ErrCleanupIncomplete, fmt.Errorf("remove quarantined destination entry: %w", err))
	}
	return nil
}

func createPartFile(ops destinationOps, dir *anchoredDir, displayDir, safeName string) (string, *os.File, error) {
	for range 100 {
		name, err := ops.generatePartName(safeName)
		if err != nil {
			return "", nil, err
		}
		if filepath.Base(name) != name || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
			return "", nil, fmt.Errorf("%w: invalid generated part name", ErrInvalidDestination)
		}
		file, err := ops.createExclusive(dir, name, filepath.Join(displayDir, name))
		if errors.Is(err, os.ErrExist) {
			continue
		}
		return name, file, err
	}
	return "", nil, fmt.Errorf("could not allocate exclusive part name after 100 attempts: %w", os.ErrExist)
}

func randomPartName(safeName string) (string, error) {
	name, err := randomPrivateName("." + safeName + ".")
	if err != nil {
		return "", err
	}
	return name + ".part", nil
}

func randomPrivateName(prefix string) (string, error) {
	suffix := make([]byte, 16)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(suffix), nil
}

func validateFinalTarget(dir *anchoredDir, name, displayPath string, overwrite bool) (targetSnapshot, error) {
	info, err := dir.lstat(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return targetSnapshot{}, nil
		}
		return targetSnapshot{}, fmt.Errorf("inspect download destination: %w", err)
	}
	if !info.regular {
		return targetSnapshot{}, fmt.Errorf("%w: %s", ErrUnsafeDestination, displayPath)
	}
	if !overwrite {
		dirID, identityErr := dir.identity()
		if identityErr != nil {
			return targetSnapshot{}, fmt.Errorf("inspect download directory identity: %w", identityErr)
		}
		return targetSnapshot{}, &DestinationExistsError{
			FinalPath: displayPath,
			Size:      info.size,
			Identity:  newArtifactIdentity(dirID, info.identity),
		}
	}
	return targetSnapshot{exists: true, identity: info.identity}, nil
}

// Commit syncs and publishes the part file using only the directory handle
// captured by OpenDestination. On Darwin and Linux, replacing an existing
// regular file uses an atomic name exchange; the displaced inode is then
// checked against the inode accepted at open time. Publishing to an absent
// name uses one atomic no-replace rename after the synced part has been closed
// and captured under a private name. A changed, symlink, or special target is
// atomically restored and rejected. Platforms without both atomic exchange and
// no-replace rename fail closed in OpenDestination.
func (d *Destination) Commit() error {
	if d == nil {
		return ErrInvalidDestination
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.commitLocked()
}

// ArtifactIdentity returns the opaque identity of a successfully committed
// destination. The zero value is returned before commit or after an abort.
func (d *Destination) ArtifactIdentity() ArtifactIdentity {
	if d == nil {
		return ArtifactIdentity{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.published {
		return ArtifactIdentity{}
	}
	if !d.publishedIDValid {
		return ArtifactIdentity{}
	}
	return newArtifactIdentity(d.dirID, d.publishedID)
}

func (d *Destination) commitLocked() error {
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
	if err := d.ops.syncFile(d.file); err != nil {
		return d.failBeforeCommit(fmt.Errorf("sync download: %w", err))
	}
	if err := d.ops.closeFile(d.file); err != nil {
		d.file = nil
		d.File = nil
		return d.failBeforeCommit(errors.Join(ErrCleanupIncomplete, fmt.Errorf("close download: %w", err)))
	}
	d.file = nil
	d.File = nil

	if d.overwrite {
		published, publishErr := d.publishOverwrite()
		if publishErr != nil {
			if !published {
				return d.failBeforeCommit(publishErr)
			}
			return d.finishPublishedError(publishErr)
		}
	} else {
		published, publishErr := d.publishAbsent(d.ops.beforeNoReplace, ErrDestinationExists)
		if publishErr != nil {
			if !published {
				return d.failBeforeCommit(publishErr)
			}
			return d.finishPublishedError(publishErr)
		}
	}

	return d.finishCommit()
}

func (d *Destination) publishAbsent(hook func(), collisionSentinel error) (bool, error) {
	if err := d.validatePartEntry(); err != nil {
		return false, err
	}
	hook()
	privatePart, err := captureNamedRegular(d.ops, d.dir, d.partName, d.PartPath, d.partID)
	if err != nil {
		return false, err
	}
	probe, err := d.dir.open(privatePart, filepath.Join(filepath.Dir(d.PartPath), privatePart))
	if err != nil {
		return false, fmt.Errorf("open published download probe: %w", err)
	}
	defer probe.Close()
	d.ops.beforeAbsentPublish()
	if err := normalizeAtomicRenameError(d.ops.renameNoReplace(d.dir, privatePart, d.finalName), privatePart, d.finalName); err != nil {
		publishErr := fmt.Errorf("atomic publish download: %w", err)
		if errors.Is(err, os.ErrExist) {
			if errors.Is(collisionSentinel, ErrDestinationChanged) {
				publishErr = d.targetRaceError()
			} else {
				publishErr = inspectDestinationCollision(d.dir, d.finalName, d.FinalPath)
			}
		}
		return false, errors.Join(
			publishErr,
			restorePrivateRegular(d.ops, d.dir, privatePart, d.partName, d.PartPath, d.partID),
		)
	}
	if err := validatePublishedEntry(d.dir, d.finalName, d.FinalPath, probe); err != nil {
		return true, errors.Join(ErrCleanupIncomplete, fmt.Errorf("download published but final identity changed: %w", err))
	}
	if err := d.capturePublishedIdentity(); err != nil {
		return true, errors.Join(ErrCleanupIncomplete, fmt.Errorf("capture published download identity: %w", err))
	}
	return true, nil
}

func (d *Destination) capturePublishedIdentity() error {
	entry, err := d.dir.lstat(d.finalName)
	if err != nil {
		return err
	}
	if !entry.regular {
		return ErrUnsafeDestination
	}
	d.publishedID = entry.identity
	d.publishedIDValid = true
	return nil
}

func inspectDestinationCollision(dir *anchoredDir, name, displayPath string) error {
	entry, err := dir.lstat(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: colliding destination disappeared: %s", ErrDestinationChanged, displayPath)
		}
		return errors.Join(
			fmt.Errorf("%w: %s", ErrDestinationChanged, displayPath),
			fmt.Errorf("inspect colliding destination: %w", err),
		)
	}
	if !entry.regular {
		return fmt.Errorf("%w: %s", ErrUnsafeDestination, displayPath)
	}
	dirID, identityErr := dir.identity()
	if identityErr != nil {
		return errors.Join(
			fmt.Errorf("%w: %s", ErrDestinationChanged, displayPath),
			fmt.Errorf("inspect collision directory identity: %w", identityErr),
		)
	}
	return &DestinationExistsError{
		FinalPath: displayPath,
		Size:      entry.size,
		Identity:  newArtifactIdentity(dirID, entry.identity),
	}
}

func (d *Destination) publishOverwrite() (bool, error) {
	if err := d.validateTargetUnchanged(); err != nil {
		return false, err
	}
	if !d.target.exists {
		return d.publishAbsent(d.ops.beforeOverwrite, ErrDestinationChanged)
	}
	if err := d.validatePartEntry(); err != nil {
		return false, err
	}
	d.ops.beforeOverwrite()
	privatePart, err := captureNamedRegular(d.ops, d.dir, d.partName, d.PartPath, d.partID)
	if err != nil {
		return false, err
	}
	probe, err := d.dir.open(privatePart, filepath.Join(filepath.Dir(d.PartPath), privatePart))
	if err != nil {
		return false, fmt.Errorf("open published download probe: %w", err)
	}
	defer probe.Close()

	if err := normalizeAtomicRenameError(d.ops.exchange(d.dir, privatePart, d.finalName), privatePart, d.finalName); err != nil {
		if errors.Is(err, ErrAtomicOverwriteUnsupported) {
			return false, errors.Join(
				err,
				restorePrivateRegular(d.ops, d.dir, privatePart, d.partName, d.PartPath, d.partID),
			)
		}
		return false, errors.Join(
			d.targetRaceError(),
			fmt.Errorf("atomic exchange download: %w", err),
			restorePrivateRegular(d.ops, d.dir, privatePart, d.partName, d.PartPath, d.partID),
		)
	}
	finalErr := validatePublishedEntry(d.dir, d.finalName, d.FinalPath, probe)
	if finalErr == nil {
		finalErr = d.capturePublishedIdentity()
	}
	displacedErr := validateNamedRegular(d.dir, privatePart, d.PartPath, d.target.identity)
	if finalErr == nil && displacedErr == nil {
		if err := deletePrivateRegular(d.ops, d.dir, privatePart, d.PartPath, d.target.identity); err != nil {
			return true, fmt.Errorf("download committed but remove displaced target: %w", err)
		}
		return true, nil
	}

	raceErr := errors.Join(finalErr, displacedErr)
	if err := normalizeAtomicRenameError(d.ops.exchange(d.dir, privatePart, d.finalName), privatePart, d.finalName); err != nil {
		return true, errors.Join(
			raceErr,
			ErrCleanupIncomplete,
			fmt.Errorf("unsafe target displaced but rollback failed: %w", err),
		)
	}
	return false, errors.Join(
		raceErr,
		restorePrivateRegular(d.ops, d.dir, privatePart, d.partName, d.PartPath, d.partID),
	)
}

// validatePublishedEntry compares the public name with an open descriptor to
// the exact producer file. This catches an immediate unlink/recreate that
// reuses the same inode, even when the replacement has the same size.
func validatePublishedEntry(dir *anchoredDir, name, displayPath string, probe *os.File) error {
	entry, err := dir.lstat(name)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrDestinationChanged, displayPath)
	}
	if !entry.regular {
		return fmt.Errorf("%w: %s", ErrUnsafeDestination, displayPath)
	}
	probeEntry, err := snapshotOpenFile(probe)
	if err != nil {
		return errors.Join(fmt.Errorf("%w: %s", ErrDestinationChanged, displayPath), err)
	}
	if !sameStrictFileIdentity(entry.identity, probeEntry.identity) || entry.size != probeEntry.size {
		return fmt.Errorf("%w: %s", ErrDestinationChanged, displayPath)
	}
	return nil
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
	d.published = true
	d.state = destinationCommitted
	syncErr := d.ops.syncDir(d.dir)
	closeErr := d.closeDir()
	if syncErr != nil {
		syncErr = fmt.Errorf("download committed but sync directory: %w", syncErr)
	}
	if closeErr != nil {
		closeErr = errors.Join(ErrCleanupIncomplete, fmt.Errorf("download committed but close directory: %w", closeErr))
	}
	joined := errors.Join(syncErr, closeErr)
	if joined != nil {
		joined = errors.Join(ErrDestinationCommitted, joined)
	}
	if errors.Is(joined, ErrCleanupIncomplete) {
		d.state = destinationCleanupIncomplete
		d.terminalErr = joined
	}
	return joined
}

func (d *Destination) finishPublishedError(publishErr error) error {
	joined := errors.Join(ErrDestinationCommitted, publishErr, d.finishCommit())
	if errors.Is(joined, ErrCleanupIncomplete) {
		d.state = destinationCleanupIncomplete
		d.terminalErr = joined
	}
	return joined
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
	case destinationCleanupIncomplete:
		return d.terminalErr
	default:
		return nil
	}
}

func (d *Destination) failBeforeCommit(primary error) error {
	cleanupErr := d.abortOpen()
	joined := errors.Join(primary, cleanupErr)
	if errors.Is(joined, ErrCleanupIncomplete) {
		d.state = destinationCleanupIncomplete
		d.terminalErr = joined
	}
	return joined
}

// Abort closes the part file and removes it. Repeated successful aborts are
// harmless; an incomplete cleanup returns the same stable error on every later
// Abort or Commit call.
func (d *Destination) Abort() error {
	if d == nil {
		return ErrInvalidDestination
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	switch d.state {
	case destinationCommitted:
		return ErrDestinationCommitted
	case destinationAborted:
		return nil
	case destinationCleanupIncomplete:
		return d.terminalErr
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
		if err := d.ops.closeFile(originalFile); err != nil && !errors.Is(err, os.ErrClosed) {
			cleanupErr = errors.Join(cleanupErr, ErrCleanupIncomplete, fmt.Errorf("close download part: %w", err))
		}
		d.file = nil
		if d.File == originalFile {
			d.File = nil
		}
	}
	if d.dir != nil && d.partName != "" {
		if err := quarantineRemove(d.ops, d.dir, d.partName, d.PartPath, d.partID); err != nil {
			cleanupErr = errors.Join(cleanupErr, ErrCleanupIncomplete, fmt.Errorf("remove download part: %w", err))
		}
	}
	if err := d.closeDir(); err != nil {
		cleanupErr = errors.Join(cleanupErr, ErrCleanupIncomplete, fmt.Errorf("close download directory: %w", err))
	}
	if cleanupErr != nil {
		d.state = destinationCleanupIncomplete
		d.terminalErr = cleanupErr
		return cleanupErr
	}
	d.state = destinationAborted
	return nil
}

func (d *Destination) closeDir() error {
	if d.dir == nil {
		return nil
	}
	dir := d.dir
	d.dir = nil
	return d.ops.closeDir(dir)
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
