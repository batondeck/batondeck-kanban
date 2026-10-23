// Package auth verifies a webhook delivery by the integration's mode before a byte of it is parsed
// (conductor functions/src/oncall/auth): an API key header, HTTP Basic, or a JWT bearer against the
// integration's JWKS. The HMAC forms are the oncall package's (oncall.VerifyBatonDeck,
// oncall.VerifyRawBody). Each verifier answers a refusal reason for the delivery log, never a hint
// the sender can iterate against.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"strings"
)

// The refusal reasons (conductor oncall.ts verifyDelivery).
const (
	ReasonAPIKeyMismatch = "api_key_mismatch"
	ReasonBasicMissing   = "basic_missing"
	ReasonBasicMalformed = "basic_malformed"
)

// VerifyAPIKey reports whether presented hashes to storedHash (hex sha256, oncall.HashAPIKey): the
// two digests compare in constant time, so neither the key nor its length leaks through timing.
func VerifyAPIKey(presented, storedHash string) bool {
	if presented == "" {
		return false
	}
	h := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare([]byte(hex.EncodeToString(h[:])), []byte(strings.ToLower(storedHash))) == 1
}

var basicHeader = regexp.MustCompile(`(?i)^Basic\s+([A-Za-z0-9+/=]+)$`)

// VerifyBasic checks an Authorization: Basic header whose password is the key; the username is
// ignored (Cloud Monitoring makes you supply one, and pinning it adds nothing the key does not).
func VerifyBasic(authorization, storedHash string) (reason string, ok bool) {
	m := basicHeader.FindStringSubmatch(authorization)
	if m == nil {
		return ReasonBasicMissing, false
	}
	decoded, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil {
		return ReasonBasicMalformed, false
	}
	_, password, found := strings.Cut(string(decoded), ":")
	if !found {
		return ReasonBasicMalformed, false
	}
	if !VerifyAPIKey(password, storedHash) {
		return ReasonAPIKeyMismatch, false
	}
	return "", true
}
