package media

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSafeUserPathRejectsTraversalAndForbiddenCharacters(t *testing.T) {
	for _, path := range []string{"../secret.txt", "bad?name.txt", "bad#name.txt"} {
		if _, err := SafeUserPath(path); err == nil {
			t.Fatalf("SafeUserPath(%q) returned nil error", path)
		}
	}
}

func TestSafeUserPathReturnsAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.bin")
	if err := os.WriteFile(path, []byte("doc"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := SafeUserPath(path)
	if err != nil {
		t.Fatalf("SafeUserPath: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("path is not absolute: %q", got)
	}
}

func TestDetectTypeFromHeadersAndExtensions(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"photo.jpg", []byte{0xff, 0xd8, 0xff, 0xe0, 'x'}, "photo"},
		{"photo.png", []byte("\x89PNG\r\n\x1a\nxxxx"), "photo"},
		{"photo.webp", []byte("RIFFxxxxWEBPdata"), "photo"},
		{"voice.ogg", []byte("OggSxxxxOpusHead"), "voice"},
		{"video.mp4", []byte("\x00\x00\x00\x18ftypmp42data"), "video"},
		{"video.webm", []byte("\x1a\x45\xdf\xa3webm"), "video"},
		{"doc.bin", []byte("anything"), "document"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.name)
			if err := os.WriteFile(path, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := DetectType(path)
			if err != nil {
				t.Fatalf("DetectType: %v", err)
			}
			if got != tc.want {
				t.Fatalf("DetectType(%q)=%q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestValidateExpectedRejectsWrongPhotoBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(path, []byte("not a jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateExpected(path, "photo", 100); err == nil {
		t.Fatalf("ValidateExpected returned nil error")
	}
}
