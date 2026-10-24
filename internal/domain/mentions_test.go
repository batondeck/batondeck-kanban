package domain

import (
	"slices"
	"testing"
)

// TestResolveMentions: a token names a member by its identity, the identity's local part, or an
// alias; an email-looking word, a non-member and text without an @ name nobody.
func TestResolveMentions(t *testing.T) {
	members := []string{"u-ada", "grace@example.com", "u-linus"}
	aliases := map[string][]string{"u-linus": {"linus@kernel.org", "Linus T"}}
	for _, c := range []struct {
		body string
		want []string
	}{
		{"no mention here", []string{}},
		{"@u-ada please look", []string{"u-ada"}},
		{"cc @Grace and @grace@example.com", []string{"grace@example.com"}},
		{"@linus thoughts?", []string{"u-linus"}},
		{"mail bob@u-ada.com", []string{}},
		{"@nobody", []string{}},
		{"@u-ada @u-ada @u-linus", []string{"u-ada", "u-linus"}},
	} {
		if got := ResolveMentions(c.body, members, aliases); !slices.Equal(got, c.want) {
			t.Errorf("ResolveMentions(%q) = %v, want %v", c.body, got, c.want)
		}
	}
}
