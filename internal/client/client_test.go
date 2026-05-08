package client

import (
	"errors"
	"testing"

	"github.com/b1rd33/tgctl-go/internal/safety"
)

func TestEnsureCredentialsMissing(t *testing.T) {
	t.Setenv("TG_API_ID", "")
	t.Setenv("TG_API_HASH", "")
	_, _, err := EnsureCredentials()
	var mc *safety.MissingCredentials
	if !errors.As(err, &mc) {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureCredentialsMalformedID(t *testing.T) {
	t.Setenv("TG_API_ID", "abc")
	t.Setenv("TG_API_HASH", "x")
	_, _, err := EnsureCredentials()
	var mc *safety.MissingCredentials
	if !errors.As(err, &mc) {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureCredentialsZeroID(t *testing.T) {
	t.Setenv("TG_API_ID", "0")
	t.Setenv("TG_API_HASH", "x")
	_, _, err := EnsureCredentials()
	var mc *safety.MissingCredentials
	if !errors.As(err, &mc) {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureCredentialsValid(t *testing.T) {
	t.Setenv("TG_API_ID", "12345")
	t.Setenv("TG_API_HASH", "deadbeef")
	id, hash, err := EnsureCredentials()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if id != 12345 || hash != "deadbeef" {
		t.Fatalf("got id=%d hash=%q", id, hash)
	}
}

func TestDisplayNameOrdering(t *testing.T) {
	cases := []struct {
		first, last, username string
		id                    int64
		want                  string
	}{
		{"Bjørn", "Müller", "bjorn", 1, "Bjørn Müller"},
		{"Bjørn", "", "bjorn", 1, "Bjørn"},
		{"", "Müller", "bjorn", 1, "Müller"},
		{"", "", "bjorn", 1, "@bjorn"},
		{"", "", "", 7, "user_7"},
	}
	for _, c := range cases {
		if got := DisplayName(c.first, c.last, c.username, c.id); got != c.want {
			t.Errorf("DisplayName(%q,%q,%q,%d) = %q, want %q",
				c.first, c.last, c.username, c.id, got, c.want)
		}
	}
}
