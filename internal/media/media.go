package media

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/b1rd33/tgctl-go/internal/safety"
)

const defaultMaxSizeMB = 100

// SafeUserPath mirrors the Python path guard for user-supplied media paths.
func SafeUserPath(value string) (string, error) {
	if strings.ContainsAny(value, "?#") {
		return "", safety.NewBadArgs("path contains forbidden character '?' or '#': %s", value)
	}
	clean := filepath.Clean(value)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", safety.NewBadArgs("path traversal is not allowed: %s", value)
	}
	abs, err := filepath.Abs(clean)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// ValidateExpected checks path safety, existence, max size, and command-specific media kind.
func ValidateExpected(path, expected string, maxSizeMB int64) (string, error) {
	abs, err := SafeUserPath(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", safety.NewBadArgs("file does not exist: %s", abs)
		}
		return "", err
	}
	if info.IsDir() {
		return "", safety.NewBadArgs("file is a directory: %s", abs)
	}
	if maxSizeMB < 0 {
		maxSizeMB = defaultMaxSizeMB
	}
	if info.Size() > maxSizeMB*1024*1024 {
		return "", safety.NewBadArgs("file %s exceeds --max-size-mb", abs)
	}
	kind, err := DetectType(abs)
	if err != nil {
		return "", err
	}
	switch expected {
	case "photo":
		if kind != "photo" {
			return "", safety.NewBadArgs("unsupported photo MIME for %s", abs)
		}
	case "voice":
		if kind != "voice" {
			return "", safety.NewBadArgs("unsupported voice MIME for %s; expected ogg/opus", abs)
		}
	case "video":
		if kind != "video" && kind != "video_note" {
			return "", safety.NewBadArgs("unsupported video MIME for %s", abs)
		}
	case "document":
	default:
		return "", safety.NewBadArgs("unsupported upload kind %q", expected)
	}
	return abs, nil
}

// DetectType ports the Python media kind classification used by tgctl.
func DetectType(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	head := buf[:n]
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case bytes.HasPrefix(head, []byte{0xff, 0xd8, 0xff}),
		bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")),
		bytes.HasPrefix(head, []byte("GIF87a")),
		bytes.HasPrefix(head, []byte("GIF89a")),
		bytes.HasPrefix(head, []byte("RIFF")) && len(head) >= 12 && bytes.Equal(head[8:12], []byte("WEBP")):
		return "photo", nil
	case bytes.HasPrefix(head, []byte("OggS")) && bytes.Contains(head, []byte("OpusHead")):
		return "voice", nil
	case bytes.HasPrefix(head, []byte("\x1a\x45\xdf\xa3")):
		return "video", nil
	case len(head) >= 12 && bytes.Equal(head[4:8], []byte("ftyp")):
		return "video", nil
	}
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return "image", nil
	case ".ogg", ".opus":
		return "voice", nil
	case ".mp4", ".mov", ".m4v", ".webm", ".mkv":
		return "video", nil
	case ".mp3", ".m4a", ".flac", ".wav":
		return "audio", nil
	case ".tgs":
		return "sticker", nil
	}
	if mt := http.DetectContentType(head); strings.HasPrefix(mt, "image/") {
		return "image", nil
	} else if strings.HasPrefix(mt, "video/") {
		return "video", nil
	} else if strings.HasPrefix(mt, "audio/") {
		return "audio", nil
	}
	return "document", nil
}
