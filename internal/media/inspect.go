package media

import (
	"errors"
	"fmt"
	"path/filepath"
)

// DownloadedArtifact is a no-follow snapshot of a direct child in an anchored
// download directory.
type DownloadedArtifact struct {
	Path     string
	Filename string
	Size     int64
}

// ArtifactIdentity is an opaque producer-issued identity for a downloaded
// artifact and its anchored output directory. Its fields are deliberately
// private so callers cannot manufacture a trusted identity from path text or
// metadata alone.
type ArtifactIdentity struct {
	directory fileIdentity
	file      fileIdentity
	valid     bool
}

func newArtifactIdentity(directory, file fileIdentity) ArtifactIdentity {
	return ArtifactIdentity{directory: directory, file: file, valid: true}
}

// InspectDownloadedArtifact verifies that artifactPath currently names a
// stable regular-file direct child of outputRoot. Directory and entry identity
// checks are anchored to open directory descriptors on supported platforms.
func InspectDownloadedArtifact(outputRoot, artifactPath string) (DownloadedArtifact, error) {
	artifact, _, err := inspectDownloadedArtifact(outputRoot, artifactPath, nil)
	return artifact, err
}

// CaptureArtifactIdentity captures an opaque identity and metadata for an
// existing artifact. Download producers should prefer identities captured from
// their already-anchored commit lifecycle; this helper exists for trusted
// producers and tests that already own a completed artifact.
func CaptureArtifactIdentity(outputRoot, artifactPath string) (ArtifactIdentity, DownloadedArtifact, error) {
	artifact, identity, err := inspectDownloadedArtifact(outputRoot, artifactPath, nil)
	return identity, artifact, err
}

// InspectDownloadedArtifactWithIdentity verifies that the current artifact is
// the same directory entry and file issued by the producer, in addition to the
// normal direct-child, no-follow, regular-file, and stability checks.
func InspectDownloadedArtifactWithIdentity(outputRoot, artifactPath string, expected ArtifactIdentity) (DownloadedArtifact, error) {
	if !expected.valid {
		return DownloadedArtifact{}, fmt.Errorf("%w: missing producer artifact identity", ErrDestinationChanged)
	}
	artifact, current, err := inspectDownloadedArtifact(outputRoot, artifactPath, nil)
	if err != nil {
		return DownloadedArtifact{}, err
	}
	if !sameFileIdentity(expected.directory, current.directory) || !sameFileIdentity(expected.file, current.file) {
		return DownloadedArtifact{}, fmt.Errorf("%w: downloaded artifact no longer matches producer identity", ErrDestinationChanged)
	}
	return artifact, nil
}

func inspectDownloadedArtifactWithHook(outputRoot, artifactPath string, afterFirstEntry func()) (DownloadedArtifact, error) {
	artifact, _, err := inspectDownloadedArtifact(outputRoot, artifactPath, afterFirstEntry)
	return artifact, err
}

func inspectDownloadedArtifact(outputRoot, artifactPath string, afterFirstEntry func()) (result DownloadedArtifact, identity ArtifactIdentity, retErr error) {
	absRoot, err := filepath.Abs(filepath.Clean(outputRoot))
	if err != nil {
		return DownloadedArtifact{}, ArtifactIdentity{}, fmt.Errorf("resolve download root: %w", err)
	}
	if !filepath.IsAbs(artifactPath) {
		return DownloadedArtifact{}, ArtifactIdentity{}, fmt.Errorf("%w: artifact path is not absolute", ErrInvalidDestination)
	}
	cleanPath := filepath.Clean(artifactPath)
	if filepath.Dir(cleanPath) != absRoot {
		return DownloadedArtifact{}, ArtifactIdentity{}, fmt.Errorf("%w: artifact is not a direct child of download root", ErrInvalidDestination)
	}
	name := filepath.Base(cleanPath)
	if name == "." || name == string(filepath.Separator) {
		return DownloadedArtifact{}, ArtifactIdentity{}, fmt.Errorf("%w: invalid artifact filename", ErrInvalidDestination)
	}

	dir, err := openAnchoredDir(absRoot)
	if err != nil {
		return DownloadedArtifact{}, ArtifactIdentity{}, fmt.Errorf("anchor download root: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, dir.close()) }()
	dirID, err := dir.identity()
	if err != nil {
		return DownloadedArtifact{}, ArtifactIdentity{}, fmt.Errorf("inspect download root: %w", err)
	}
	first, err := dir.lstat(name)
	if err != nil {
		return DownloadedArtifact{}, ArtifactIdentity{}, fmt.Errorf("inspect downloaded artifact: %w", err)
	}
	if !first.regular {
		return DownloadedArtifact{}, ArtifactIdentity{}, fmt.Errorf("%w: downloaded artifact is not regular", ErrUnsafeDestination)
	}
	if afterFirstEntry != nil {
		afterFirstEntry()
	}
	second, err := dir.lstat(name)
	if err != nil {
		return DownloadedArtifact{}, ArtifactIdentity{}, errors.Join(ErrDestinationChanged, err)
	}
	if !second.regular {
		return DownloadedArtifact{}, ArtifactIdentity{}, fmt.Errorf("%w: downloaded artifact changed to a non-regular entry", ErrUnsafeDestination)
	}
	if !sameFileIdentity(first.identity, second.identity) || first.size != second.size {
		return DownloadedArtifact{}, ArtifactIdentity{}, fmt.Errorf("%w: downloaded artifact changed during inspection", ErrDestinationChanged)
	}

	currentDir, err := openAnchoredDir(absRoot)
	if err != nil {
		return DownloadedArtifact{}, ArtifactIdentity{}, errors.Join(ErrDestinationChanged, err)
	}
	currentID, identityErr := currentDir.identity()
	closeErr := currentDir.close()
	if identityErr != nil || closeErr != nil {
		return DownloadedArtifact{}, ArtifactIdentity{}, errors.Join(identityErr, closeErr)
	}
	if !sameFileIdentity(dirID, currentID) {
		return DownloadedArtifact{}, ArtifactIdentity{}, fmt.Errorf("%w: download root changed during inspection", ErrDestinationChanged)
	}
	return DownloadedArtifact{Path: cleanPath, Filename: name, Size: second.size}, newArtifactIdentity(dirID, second.identity), nil
}
