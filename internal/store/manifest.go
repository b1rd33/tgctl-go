package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ArchiveManifest is a portable description of one local Telegram export.
// Media paths are always relative to the account media root.
type ArchiveManifest struct {
	Version   int                   `json:"version"`
	Account   string                `json:"account,omitempty"`
	ChatID    int64                 `json:"chat_id"`
	Format    string                `json:"format"`
	Output    string                `json:"output,omitempty"`
	CreatedAt string                `json:"created_at"`
	HasHashes bool                  `json:"has_hashes"`
	Items     []ArchiveManifestItem `json:"items"`
}

type ArchiveManifestItem struct {
	MessageID int64  `json:"message_id"`
	GroupedID int64  `json:"grouped_id,omitempty"`
	MediaPath string `json:"media_path,omitempty"`
	Present   bool   `json:"present"`
	Size      int64  `json:"size,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

type ArchiveManifestWarnings struct {
	Warnings []string
}

// BuildArchiveManifest derives a manifest without writing anything. Missing
// or unreadable media becomes a warning so a single broken artifact does not
// invalidate the rest of the export.
func BuildArchiveManifest(rows []Message, account string, chatID int64, format, output, mediaRoot string, hash bool) (ArchiveManifest, []string, error) {
	manifest := ArchiveManifest{
		Version: 1, Account: account, ChatID: chatID, Format: format, Output: output,
		CreatedAt: time.Now().UTC().Format(time.RFC3339), HasHashes: hash,
		Items: make([]ArchiveManifestItem, 0, len(rows)),
	}
	var warnings []string
	for _, row := range rows {
		item := ArchiveManifestItem{MessageID: row.MessageID, GroupedID: row.GroupedID}
		if row.MediaPath != nil && *row.MediaPath != "" {
			rel, err := safeRelativeMediaPath(mediaRoot, *row.MediaPath)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("message %d media path omitted: %v", row.MessageID, err))
			} else {
				item.MediaPath = rel
				path := filepath.Join(mediaRoot, filepath.FromSlash(rel))
				info, statErr := os.Stat(path)
				if statErr != nil {
					warnings = append(warnings, fmt.Sprintf("message %d media %s unavailable", row.MessageID, rel))
				} else if !info.Mode().IsRegular() {
					warnings = append(warnings, fmt.Sprintf("message %d media %s is not a regular file", row.MessageID, rel))
				} else {
					item.Present = true
					item.Size = info.Size()
					if hash {
						item.SHA256, err = sha256File(path)
						if err != nil {
							item.Present = false
							warnings = append(warnings, fmt.Sprintf("message %d media %s could not be hashed", row.MessageID, rel))
						}
					}
				}
			}
		}
		manifest.Items = append(manifest.Items, item)
	}
	return manifest, warnings, nil
}

func safeRelativeMediaPath(root, path string) (string, error) {
	absRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", errors.New("path escapes account media root")
	}
	return filepath.ToSlash(rel), nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func WriteArchiveManifest(w io.Writer, manifest ArchiveManifest) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(manifest)
}

type ArchiveVerifyResult struct {
	Checked int      `json:"checked"`
	Missing []string `json:"missing,omitempty"`
	Changed []string `json:"changed,omitempty"`
	Extra   []string `json:"extra,omitempty"`
}

// VerifyArchiveManifest checks media presence, size/hash identity, and files
// present under the media root that were not recorded in the manifest.
func VerifyArchiveManifest(manifestPath, mediaRoot string) (ArchiveManifest, ArchiveVerifyResult, error) {
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		return ArchiveManifest{}, ArchiveVerifyResult{}, err
	}
	var manifest ArchiveManifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		return ArchiveManifest{}, ArchiveVerifyResult{}, fmt.Errorf("invalid archive manifest: %w", err)
	}
	if manifest.Version != 1 {
		return manifest, ArchiveVerifyResult{}, fmt.Errorf("unsupported archive manifest version %d", manifest.Version)
	}
	result := ArchiveVerifyResult{}
	expected := make(map[string]struct{})
	for _, item := range manifest.Items {
		if item.MediaPath == "" {
			continue
		}
		rel, err := cleanManifestPath(item.MediaPath)
		if err != nil {
			return manifest, result, err
		}
		expected[rel] = struct{}{}
		result.Checked++
		path := filepath.Join(mediaRoot, filepath.FromSlash(rel))
		info, statErr := os.Stat(path)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				result.Missing = append(result.Missing, rel)
				continue
			}
			result.Changed = append(result.Changed, rel)
			continue
		}
		if !info.Mode().IsRegular() || !item.Present || info.Size() != item.Size {
			result.Changed = append(result.Changed, rel)
			continue
		}
		if item.SHA256 != "" {
			digest, hashErr := sha256File(path)
			if hashErr != nil || !strings.EqualFold(digest, item.SHA256) {
				result.Changed = append(result.Changed, rel)
			}
		}
	}
	walkErr := filepath.WalkDir(mediaRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, relErr := filepath.Rel(mediaRoot, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if _, ok := expected[rel]; !ok {
			result.Extra = append(result.Extra, rel)
		}
		return nil
	})
	if walkErr != nil {
		return manifest, result, walkErr
	}
	sort.Strings(result.Missing)
	sort.Strings(result.Changed)
	sort.Strings(result.Extra)
	return manifest, result, nil
}

func cleanManifestPath(path string) (string, error) {
	if path == "" || filepath.IsAbs(path) || strings.Contains(path, "\\") {
		return "", errors.New("manifest media path must be relative and slash-separated")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("manifest media path escapes account media root")
	}
	return clean, nil
}
