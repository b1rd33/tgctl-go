package commands

import "testing"

func TestParsePositiveDecimalStrictWholeToken(t *testing.T) {
	for _, raw := range []string{"", "0", "-1", "+1", " 1", "1 ", "1x", "1 x", "9223372036854775808"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := parsePositiveDecimal(raw, "message-id"); err == nil {
				t.Fatalf("parsePositiveDecimal(%q) unexpectedly succeeded", raw)
			}
		})
	}
	for raw, want := range map[string]int64{"1": 1, "42": 42, "9223372036854775807": 9223372036854775807} {
		if got, err := parsePositiveDecimal(raw, "message-id"); err != nil || got != want {
			t.Fatalf("parsePositiveDecimal(%q)=(%d, %v), want (%d, nil)", raw, got, err, want)
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
	got, err := parseIntCSV("1,2,9223372036854775807")
	if err != nil || len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 9223372036854775807 {
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
