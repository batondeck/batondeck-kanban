package oncall

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Webhook HMAC verification (conductor functions/src/oncall/auth/hmac.ts:3-9), two forms:
//   - BatonDeck HMAC (the generic connector): `v0=hex(hmac-sha256(secret, "<unix-seconds>.<rawBody>"))`
//     with the timestamp within Tolerance of now;
//   - raw-body HMAC (PagerDuty's own): `v1=hex(hmac-sha256(secret, rawBody))`, possibly several
//     comma-separated, no timestamp (a delivery id's idempotency bounds replay instead).
// Both accept a signature by any of the secrets Secrets returns: the current one, and the previous one
// while its rotation window is open. Every comparison runs, so timing does not say which matched.

// RotationGrace is how long a rotated-out secret keeps verifying (conductor ROTATION_GRACE_MS).
const RotationGrace = 24 * time.Hour

// Tolerance is the BatonDeck form's timestamp window either side of now.
const Tolerance = 300 * time.Second

// The refusal reasons a verification returns (conductor VerifyResult reasons).
const (
	ReasonMissingSignature   = "missing_signature"
	ReasonMissingTimestamp   = "missing_timestamp"
	ReasonBadTimestamp       = "bad_timestamp"
	ReasonTimestampSkew      = "timestamp_skew"
	ReasonMalformedSignature = "malformed_signature"
	ReasonNoSecrets          = "no_secrets"
	ReasonSignatureMismatch  = "signature_mismatch"
)

// Auth is an integration's stored auth block (the integrations.auth column as JSON): its mode and
// that mode's material, every secret sealed (Seal) or hashed before it is stored.
type Auth struct {
	Mode AuthMode  `json:"mode"`
	HMAC *HMACAuth `json:"hmac,omitempty"`
	// APIKeyHash is hex sha256 of the key an api_key or basic source presents.
	APIKeyHash string   `json:"apiKeyHash,omitempty"`
	JWT        *JWTAuth `json:"jwt,omitempty"`
}

// HMACAuth is an hmac integration's secrets. Times are Unix milliseconds.
type HMACAuth struct {
	// Current is the live secret's envelope.
	Current string `json:"currentEncrypted"`
	// Previous is the rotated-out secret's envelope, "" when there is none; it verifies until
	// PreviousExpiresAt.
	Previous          string `json:"previousEncrypted,omitempty"`
	PreviousExpiresAt int64  `json:"previousExpiresAt,omitempty"`
	RotatedAt         int64  `json:"rotatedAt"`
	// ExternallyIssued: the source minted the secret (PagerDuty), so it was never shown back.
	ExternallyIssued bool `json:"externallyIssued,omitempty"`
}

// JWTAuth is a jwt integration's issuer, audience and key set.
type JWTAuth struct {
	Issuer   string `json:"issuer"`
	Audience string `json:"audience"`
	JWKSURL  string `json:"jwksUrl"`
}

// DualAcceptOpen reports whether a rotated-out secret still verifies at now.
func (h HMACAuth) DualAcceptOpen(now time.Time) bool {
	return h.Previous != "" && now.UnixMilli() < h.PreviousExpiresAt
}

// Secrets opens the secrets a delivery at now may be signed with: the current one, and the previous
// one while DualAcceptOpen. aad is SecretAAD of the integration.
func (h HMACAuth) Secrets(kek KEK, aad string, now time.Time) ([][]byte, error) {
	envelopes := []string{h.Current}
	if h.DualAcceptOpen(now) {
		envelopes = append(envelopes, h.Previous)
	}
	out := make([][]byte, 0, len(envelopes))
	for _, e := range envelopes {
		s, err := Open(kek, e, aad)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

var digits = regexp.MustCompile(`^\d{1,15}$`)

// VerifyBatonDeck checks the BatonDeck form: ok, or the reason it refused.
func VerifyBatonDeck(rawBody []byte, timestamp, signature string, secrets [][]byte, now time.Time) (reason string, ok bool) {
	switch {
	case signature == "":
		return ReasonMissingSignature, false
	case timestamp == "":
		return ReasonMissingTimestamp, false
	case !digits.MatchString(timestamp):
		return ReasonBadTimestamp, false
	}
	ts, _ := strconv.ParseInt(timestamp, 10, 64)
	if skew := now.Sub(time.Unix(ts, 0)); skew > Tolerance || skew < -Tolerance {
		return ReasonTimestampSkew, false
	}
	base := append([]byte(timestamp+"."), rawBody...)
	return verify(signature, "v0=", base, secrets)
}

// VerifyRawBody checks the raw-body form (PagerDuty's x-pagerduty-signature): ok, or the reason.
func VerifyRawBody(rawBody []byte, signature string, secrets [][]byte) (reason string, ok bool) {
	if signature == "" {
		return ReasonMissingSignature, false
	}
	return verify(signature, "v1=", rawBody, secrets)
}

func verify(header, prefix string, message []byte, secrets [][]byte) (string, bool) {
	var presented [][]byte
	for _, p := range strings.Split(header, ",") {
		if p = strings.TrimSpace(p); strings.HasPrefix(p, prefix) {
			presented = append(presented, hexOrRaw(p[len(prefix):]))
		}
	}
	if len(presented) == 0 {
		return ReasonMalformedSignature, false
	}
	if len(secrets) == 0 {
		return ReasonNoSecrets, false
	}
	matched := false
	for _, s := range secrets {
		m := hmac.New(sha256.New, s)
		m.Write(message)
		want := m.Sum(nil)
		for _, sig := range presented {
			if constantTimeEqual(sig, want) {
				matched = true
			}
		}
	}
	if !matched {
		return ReasonSignatureMismatch, false
	}
	return "", true
}

// hexOrRaw decodes a hex signature; anything that is not clean hex is compared as its bytes, and so
// never matches a digest.
func hexOrRaw(s string) []byte {
	if b, err := hex.DecodeString(s); err == nil {
		return b
	}
	return []byte(s)
}

// constantTimeEqual compares in time independent of where a and b differ, and of a length mismatch:
// unequal lengths compare their digests (always unequal) for the same cost.
func constantTimeEqual(a, b []byte) bool {
	if len(a) == len(b) {
		return subtle.ConstantTimeCompare(a, b) == 1
	}
	ha, hb := sha256.Sum256(a), sha256.Sum256(b)
	_ = subtle.ConstantTimeCompare(ha[:], hb[:])
	return false
}

// HashAPIKey is the stored form of an api_key or basic key: hex sha256, the plaintext never kept.
func HashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}
