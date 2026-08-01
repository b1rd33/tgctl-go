package commands

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestParsePositiveInt32DecimalStrictWholeToken(t *testing.T) {
	for _, raw := range []string{"", "0", "-1", "+1", " 1", "1 ", "1x", "1 x", "2147483648", "9223372036854775808"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := parsePositiveInt32Decimal(raw, "message-id"); err == nil {
				t.Fatalf("parsePositiveInt32Decimal(%q) unexpectedly succeeded", raw)
			}
		})
	}
	for raw, want := range map[string]int64{"1": 1, "42": 42, "2147483647": 2147483647} {
		if got, err := parsePositiveInt32Decimal(raw, "message-id"); err != nil || got != want {
			t.Fatalf("parsePositiveInt32Decimal(%q)=(%d, %v), want (%d, nil)", raw, got, err, want)
		}
	}
}

func TestOptionalInt32FlagBounds(t *testing.T) {
	for _, value := range []int64{-1, 2147483648} {
		if err := validateOptionalPositiveInt32(value, "--reply-to"); err == nil {
			t.Fatalf("validateOptionalPositiveInt32(%d) unexpectedly succeeded", value)
		}
	}
	for _, value := range []int64{0, 1, 2147483647} {
		if err := validateOptionalPositiveInt32(value, "--reply-to"); err != nil {
			t.Fatalf("validateOptionalPositiveInt32(%d): %v", value, err)
		}
	}
}

func TestParseIntCSVRejectsMalformedMessageIDs(t *testing.T) {
	for _, raw := range []string{"", "1,,2", "1, 2", "1,2 ", "1,2x", "1,0", "1,-2", "1,+2", "1,9223372036854775808"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := parseIntCSV(raw); err == nil {
				t.Fatalf("parseIntCSV(%q) unexpectedly succeeded", raw)
			}
		})
	}
	got, err := parseIntCSV("1,2,2147483647")
	if err != nil || len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 2147483647 {
		t.Fatalf("valid parseIntCSV = (%v, %v)", got, err)
	}
}

func TestSignedTelegramIDsRetainNegativeValues(t *testing.T) {
	got, err := parseSignedNonZeroCSV("-100123,42", "chat-id")
	if err != nil || len(got) != 2 || got[0] != -100123 || got[1] != 42 {
		t.Fatalf("parseSignedNonZeroCSV = (%v, %v)", got, err)
	}
	for _, raw := range []string{"0", "+1", "- 1", "1x", " 1"} {
		if _, err := parseSignedNonZeroCSV(raw, "chat-id"); err == nil {
			t.Fatalf("parseSignedNonZeroCSV(%q) unexpectedly succeeded", raw)
		}
	}
}

func TestDownloadMessageIDRejectsWhitespaceAndSigns(t *testing.T) {
	for _, raw := range []string{" 1", "1 ", "+1", "-1", "0", "2147483648"} {
		if _, err := parseDownloadMessageID(raw); err == nil {
			t.Fatalf("parseDownloadMessageID(%q) unexpectedly succeeded", raw)
		}
	}
	if got, err := parseDownloadMessageID("2147483647"); err != nil || got != 2147483647 {
		t.Fatalf("parseDownloadMessageID(max)=(%d, %v)", got, err)
	}
}

func TestFolderCSVUsesFolderDiagnostic(t *testing.T) {
	_, err := parsePositiveInt32CSV("1,2147483648", "folder-id")
	if err == nil || !strings.Contains(err.Error(), "folder-id") || strings.Contains(err.Error(), "message-id") {
		t.Fatalf("folder CSV error=%q", err)
	}
}

func TestDefaultedInt32LimitSemantics(t *testing.T) {
	for _, value := range []int{0, -1} {
		got, err := defaultedInt32Limit(value, 200, "--limit")
		if err != nil || got != 200 {
			t.Fatalf("defaultedInt32Limit(%d)=(%d, %v), want (200, nil)", value, got, err)
		}
	}
	got, err := defaultedInt32Limit(2147483647, 200, "--limit")
	if err != nil || got != 2147483647 {
		t.Fatalf("max int32=(%d, %v)", got, err)
	}
	if strconv.IntSize > 32 {
		over := int64(math.MaxInt32)
		over++
		if _, err := defaultedInt32Limit(int(over), 200, "--limit"); err == nil {
			t.Fatal("overflow unexpectedly accepted")
		}
	}
}
