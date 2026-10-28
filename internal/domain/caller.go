package domain

import (
	"strings"
	"unicode"
	"unicode/utf16"
)

// Caller is the authenticated principal every service call carries (ADR-0003).
type Caller struct {
	Identity string
	// Agent tells the agents of one identity apart (audit B1): the API token's label, else the
	// OAuth client's registered client_name, else the MCP initialize clientInfo.name, each through
	// AgentLabel; "" for a browser session, or for a token with no label of its own
	// (DefaultTokenLabel) outside MCP.
	Agent          string
	Role           string
	WorkspaceAdmin bool
	TokenScope     string
	// Via is how the caller authenticated: ViaAPIToken, ViaOAuth, or "" (a browser session).
	Via string
	// OAuthClientID is a ViaOAuth caller's client.
	OAuthClientID string
	// Labelled is true when Agent is the bearer token's own label, minted by its owner, rather than
	// a name the client registered or declared: only a labelled caller holds a label (P-277).
	Labelled bool
}

// How a caller authenticated (Caller.Via).
const (
	ViaAPIToken = "api_token"
	ViaOAuth    = "oauth"
)

// DefaultTokenLabel is the label `token create` gives a token minted without --label. It names no
// agent, so a caller on such a token is told apart by its client's name instead.
const DefaultTokenLabel = "cli"

// MaxAgentChars caps an agent name, in UTF-16 code units as LabelArg counts them, so that every
// agent name fits where an assignee or a label goes.
const MaxAgentChars = 64

// AgentLabel is s as an agent name: control characters dropped (a NUL cannot reach Postgres
// text), trimmed, and cut to MaxAgentChars without splitting a character.
func AgentLabel(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
	n := 0
	for i, r := range s {
		n += utf16.RuneLen(r)
		if n > MaxAgentChars {
			return strings.TrimSpace(s[:i])
		}
	}
	return s
}

// TokenAgent is the agent an API token's label names: the label through AgentLabel, or "" for
// DefaultTokenLabel, which names none.
func TokenAgent(label string) string {
	if a := AgentLabel(label); a != DefaultTokenLabel {
		return a
	}
	return ""
}
