package store

import "testing"

// TestNormalize pins the answer-canonicalisation rules. Answer checking must
// be deterministic, so this behaviour is contract, not incidental.
func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"  Hello   World  ": "hello world",
		"42":                "42",
		"FOO":               "foo",
		"\tTrue\n":          "true",
		"a  b\tc":           "a b c",
		"":                  "",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}
