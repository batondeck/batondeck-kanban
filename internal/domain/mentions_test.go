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

// TestMentionNames (P-348): a token names a name by the name's normalised value or its local part,
// the match ResolveMentions reads an alias by, and a name is compared whole: a label with a
// non-handle character is never cut to its first handle.
func TestMentionNames(t *testing.T) {
	for _, c := range []struct {
		token, name string
		want        bool
	}{
		{"pbob", "pbob", true},
		{"pbob", " PBob ", true},
		{"pbob", "pbob@x.example", true}, // the local part (Fable A1)
		{"pbob@x.example", "pbob@x.example", true},
		{"pbob@x.example", "pbob", false}, // a whole email does not name its local part's owner
		{"pt", "pt bob", false},           // never truncated (Fable A2)
		{"pt", "pt.bob", false},
		{"pt", "pt", true},
		{"", "", false},
		{"x", "  ", false},
	} {
		if got := MentionNames(c.token, c.name); got != c.want {
			t.Errorf("MentionNames(%q, %q) = %v, want %v", c.token, c.name, got, c.want)
		}
	}
}
