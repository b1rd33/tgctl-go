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

// InspectDownloadedArtifact verifies that artifactPath currently names a
// stable regular-file direct child of outputRoot. Directory and entry identity
// checks are anchored to open directory descriptors on supported platforms.
func InspectDownloadedArtifact(outputRoot, artifactPath string) (DownloadedArtifact, error) {
	return inspectDownloadedArtifactWithHook(outputRoot, artifactPath, nil)
}

func inspectDownloadedArtifactWithHook(outputRoot, artifactPath string, afterFirstEntry func()) (result DownloadedArtifact, retErr error) {
	absRoot, err := filepath.Abs(filepath.Clean(outputRoot))
	if err != nil {
		return DownloadedArtifact{}, fmt.Errorf("resolve download root: %w", err)
	}
	if !filepath.IsAbs(artifactPath) {
		return DownloadedArtifact{}, fmt.Errorf("%w: artifact path is not absolute", ErrInvalidDestination)
	}
	cleanPath := filepath.Clean(artifactPath)
	if filepath.Dir(cleanPath) != absRoot {
		return DownloadedArtifact{}, fmt.Errorf("%w: artifact is not a direct child of download root", ErrInvalidDestination)
	}
	name := filepath.Base(cleanPath)
	if name == "." || name == string(filepath.Separator) {
		return DownloadedArtifact{}, fmt.Errorf("%w: invalid artifact filename", ErrInvalidDestination)
	}

	dir, err := openAnchoredDir(absRoot)
	if err != nil {
		return DownloadedArtifact{}, fmt.Errorf("anchor download root: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, dir.close()) }()
	dirID, err := dir.identity()
	if err != nil {
		return DownloadedArtifact{}, fmt.Errorf("inspect download root: %w", err)
	}
	first, err := dir.lstat(name)
	if err != nil {
		return DownloadedArtifact{}, fmt.Errorf("inspect downloaded artifact: %w", err)
	}
	if !first.regular {
		return DownloadedArtifact{}, fmt.Errorf("%w: downloaded artifact is not regular", ErrUnsafeDestination)
	}
	if afterFirstEntry != nil {
		afterFirstEntry()
	}
	second, err := dir.lstat(name)
	if err != nil {
		return DownloadedArtifact{}, errors.Join(ErrDestinationChanged, err)
	}
	if !second.regular {
		return DownloadedArtifact{}, fmt.Errorf("%w: downloaded artifact changed to a non-regular entry", ErrUnsafeDestination)
	}
	if !sameFileIdentity(first.identity, second.identity) || first.size != second.size {
		return DownloadedArtifact{}, fmt.Errorf("%w: downloaded artifact changed during inspection", ErrDestinationChanged)
	}

	currentDir, err := openAnchoredDir(absRoot)
	if err != nil {
		return DownloadedArtifact{}, errors.Join(ErrDestinationChanged, err)
	}
	currentID, identityErr := currentDir.identity()
	closeErr := currentDir.close()
	if identityErr != nil || closeErr != nil {
		return DownloadedArtifact{}, errors.Join(identityErr, closeErr)
	}
	if !sameFileIdentity(dirID, currentID) {
		return DownloadedArtifact{}, fmt.Errorf("%w: download root changed during inspection", ErrDestinationChanged)
	}
	return DownloadedArtifact{Path: cleanPath, Filename: name, Size: second.size}, nil
}
