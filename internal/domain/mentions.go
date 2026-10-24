package domain

import (
	"regexp"
	"strings"
)

// mentionRE is conductor's MENTION_RE (src/domain/mentions.ts): an @ not preceded by a word, dot or
// email character, then a handle, or a whole email address.
var mentionRE = regexp.MustCompile(`(?:^|[^a-zA-Z0-9._%+@-])@([a-zA-Z0-9](?:[a-zA-Z0-9._%+-]{0,62}[a-zA-Z0-9])?(?:@[a-zA-Z0-9.-]{1,251}\.[a-zA-Z]{2,24})?)`)

func normMention(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func localPart(s string) string {
	if i := strings.IndexByte(s, '@'); i >= 0 {
		return s[:i]
	}
	return s
}

// MentionTokens are the body's @tokens, lower-cased, each once; none when the body mentions nobody.
func MentionTokens(body string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range mentionRE.FindAllStringSubmatch(body, -1) {
		if tok := normMention(m[1]); tok != "" && !seen[tok] {
			seen[tok] = true
			out = append(out, tok)
		}
	}
	return out
}

// ResolveMentions is conductor's resolveMentions: the members, in members' order, that a token of
// the body names, by the member identity, an alias (an account's email or display name) or the
// local part of either. Only a member can be selected, so text cannot invent one.
func ResolveMentions(body string, members []string, aliases map[string][]string) []string {
	tokens := map[string]bool{}
	for _, t := range MentionTokens(body) {
		tokens[t] = true
	}
	out := []string{}
	if len(tokens) == 0 {
		return out
	}
	named := func(s string) bool {
		n := normMention(s)
		return n != "" && (tokens[n] || tokens[localPart(n)])
	}
	seen := map[string]bool{}
	for _, id := range members {
		if seen[id] {
			continue
		}
		hit := named(id)
		for _, a := range aliases[id] {
			hit = hit || named(a)
		}
		if hit {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
