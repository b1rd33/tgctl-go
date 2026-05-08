package text

import "testing"

func TestStripAccents(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"plain", "plain"},
		{"Bjørn Müller", "bjørn muller"},
		{"Bjarne", "bjarne"},
		{"Ångström", "angstrom"},
		{"naïve café", "naive cafe"},
	}
	for _, c := range cases {
		got := StripAccents(c.in)
		if got != c.want {
			t.Errorf("StripAccents(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
