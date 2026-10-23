package oncall_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/oncall"
)

func keyring(t *testing.T, fill byte) *core.Keyring {
	t.Helper()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32))
	k, err := core.OpenKeyring("", func(name string) (string, bool) { return key, name == core.MasterKeyEnv })
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// TestEnvelope: a sealed secret is conductor's seven-segment enc1 string, carries no plaintext, and
// opens with the same keyring and AAD only — a wrong AAD, another keyring, a flipped segment and a
// wrong format all fail with the one ErrEnvelope.
func TestEnvelope(t *testing.T) {
	k := keyring(t, 7)
	secret := []byte("whsec_fixture_not_a_real_secret")
	env, err := oncall.Seal(k, secret, oncall.SecretAAD("I-1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(env, oncall.EnvelopePrefix) || strings.Count(env, ":") != 6 || strings.Contains(env, string(secret)) {
		t.Fatalf("envelope %q: want enc1 and seven segments, no plaintext", env)
	}
	got, err := oncall.Open(k, env, oncall.SecretAAD("I-1"))
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("Open = %q, %v; want the secret (the positive control)", got, err)
	}
	again, _ := oncall.Seal(k, secret, oncall.SecretAAD("I-1"))
	if again == env {
		t.Fatal("two seals of one secret are equal: the DEK and nonces are not fresh")
	}
	parts := strings.Split(env, ":")
	flipped := slices.Clone(parts)
	b, _ := base64.RawURLEncoding.DecodeString(flipped[5])
	b[0] ^= 1
	flipped[5] = base64.RawURLEncoding.EncodeToString(b)
	for name, open := range map[string]func() ([]byte, error){
		"another integration's AAD": func() ([]byte, error) { return oncall.Open(k, env, oncall.SecretAAD("I-2")) },
		"another keyring":           func() ([]byte, error) { return oncall.Open(keyring(t, 8), env, oncall.SecretAAD("I-1")) },
		"a flipped ciphertext byte": func() ([]byte, error) { return oncall.Open(k, strings.Join(flipped, ":"), oncall.SecretAAD("I-1")) },
		"another version":           func() ([]byte, error) { return oncall.Open(k, "enc2"+env[4:], oncall.SecretAAD("I-1")) },
		"six segments":              func() ([]byte, error) { return oncall.Open(k, strings.Join(parts[:6], ":"), oncall.SecretAAD("I-1")) },
		"the plaintext itself":      func() ([]byte, error) { return oncall.Open(k, string(secret), oncall.SecretAAD("I-1")) },
	} {
		if pt, err := open(); !errors.Is(err, oncall.ErrEnvelope) || pt != nil {
			t.Errorf("%s: Open = %q, %v; want ErrEnvelope", name, pt, err)
		}
	}
}

func sign(secret, message []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(message)
	return hex.EncodeToString(m.Sum(nil))
}

func signV0(secret []byte, ts int64, body []byte) (string, string) {
	t := strconv.FormatInt(ts, 10)
	return t, "v0=" + sign(secret, append([]byte(t+"."), body...))
}

// TestDualAccept (AC2's rule): after a rotation at r, a delivery signed with the old secret verifies
// while now < r + RotationGrace and is refused signature_mismatch from then on; the new secret
// verifies throughout.
func TestDualAccept(t *testing.T) {
	k := keyring(t, 7)
	aad := oncall.SecretAAD("I-9")
	oldSecret, newSecret := []byte("whsec_old_fixture"), []byte("whsec_new_fixture")
	oldEnv, _ := oncall.Seal(k, oldSecret, aad)
	newEnv, _ := oncall.Seal(k, newSecret, aad)
	rotated := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	h := oncall.HMACAuth{Current: newEnv, Previous: oldEnv, PreviousExpiresAt: rotated.Add(oncall.RotationGrace).UnixMilli(), RotatedAt: rotated.UnixMilli()}
	body := []byte(`{"title":"disk full"}`)
	for _, c := range []struct {
		name   string
		at     time.Time
		oldOK  bool
		window bool
	}{
		{"at the rotation", rotated, true, true},
		{"one millisecond before the window ends", rotated.Add(oncall.RotationGrace - time.Millisecond), true, true},
		{"as the window ends", rotated.Add(oncall.RotationGrace), false, false},
		{"a day after", rotated.Add(2 * oncall.RotationGrace), false, false},
	} {
		secrets, err := h.Secrets(k, aad, c.at)
		if err != nil {
			t.Fatal(err)
		}
		if got := h.DualAcceptOpen(c.at); got != c.window || len(secrets) != map[bool]int{true: 2, false: 1}[c.window] {
			t.Errorf("%s: window open %v with %d secrets, want %v", c.name, got, len(secrets), c.window)
		}
		ts, sig := signV0(oldSecret, c.at.Unix(), body)
		reason, ok := oncall.VerifyBatonDeck(body, ts, sig, secrets, c.at)
		if ok != c.oldOK || !ok && reason != oncall.ReasonSignatureMismatch {
			t.Errorf("%s: the old secret verifies %v (%s), want %v", c.name, ok, reason, c.oldOK)
		}
		ts, sig = signV0(newSecret, c.at.Unix(), body)
		if reason, ok := oncall.VerifyBatonDeck(body, ts, sig, secrets, c.at); !ok {
			t.Errorf("%s: the new secret is refused (%s); it verifies throughout", c.name, reason)
		}
		pd := "v1=" + sign(oldSecret, body)
		if _, ok := oncall.VerifyRawBody(body, pd, secrets); ok != c.oldOK {
			t.Errorf("%s: the old secret's raw-body signature verifies %v, want %v", c.name, ok, c.oldOK)
		}
		t.Logf("%s: old secret %v, new secret true, window open %v", c.name, ok, c.window)
	}
	revoked := h
	revoked.Previous, revoked.PreviousExpiresAt = "", 0
	if s, _ := revoked.Secrets(k, aad, rotated); len(s) != 1 {
		t.Errorf("a revoked previous secret still yields %d secrets", len(s))
	}
}

// TestVerifyRefusals: each refusal reason of the two forms, and the positive control beside it.
func TestVerifyRefusals(t *testing.T) {
	secret := []byte("whsec_fixture")
	secrets := [][]byte{secret}
	now := time.Unix(1_800_000_000, 0)
	body := []byte(`{"a":1}`)
	ts, sig := signV0(secret, now.Unix(), body)
	if r, ok := oncall.VerifyBatonDeck(body, ts, sig, secrets, now); !ok {
		t.Fatalf("a good v0 signature is refused: %s", r)
	}
	_, stale := signV0(secret, now.Add(-oncall.Tolerance-time.Second).Unix(), body)
	staleTs := strconv.FormatInt(now.Add(-oncall.Tolerance-time.Second).Unix(), 10)
	for _, c := range []struct {
		name, ts, sig, want string
		body                []byte
		secrets             [][]byte
	}{
		{"no signature", ts, "", oncall.ReasonMissingSignature, body, secrets},
		{"no timestamp", "", sig, oncall.ReasonMissingTimestamp, body, secrets},
		{"a non-numeric timestamp", "12ab", sig, oncall.ReasonBadTimestamp, body, secrets},
		{"a timestamp past the tolerance", staleTs, stale, oncall.ReasonTimestampSkew, body, secrets},
		{"a v1 header on the v0 form", ts, "v1=" + sign(secret, body), oncall.ReasonMalformedSignature, body, secrets},
		{"no secrets", ts, sig, oncall.ReasonNoSecrets, body, nil},
		{"one byte of the body changed", ts, sig, oncall.ReasonSignatureMismatch, []byte(`{"a":2}`), secrets},
		{"another secret", ts, sig, oncall.ReasonSignatureMismatch, body, [][]byte{[]byte("whsec_other")}},
	} {
		if r, ok := oncall.VerifyBatonDeck(c.body, c.ts, c.sig, c.secrets, now); ok || r != c.want {
			t.Errorf("%s: %q, %v; want %q", c.name, r, ok, c.want)
		}
	}
	multi := "v1=deadbeef, v1=" + sign(secret, body)
	if r, ok := oncall.VerifyRawBody(body, multi, secrets); !ok {
		t.Errorf("the second of two v1 signatures is refused: %s", r)
	}
	if r, ok := oncall.VerifyRawBody(body, "", secrets); ok || r != oncall.ReasonMissingSignature {
		t.Errorf("no v1 signature: %q, %v", r, ok)
	}
	if r, ok := oncall.VerifyRawBody(body, "v1=zz-not-hex", secrets); ok || r != oncall.ReasonSignatureMismatch {
		t.Errorf("a non-hex v1 signature: %q, %v", r, ok)
	}
}

// TestSources: the catalog is the eight connectors, each with at least one auth mode it can
// produce, and only the webhook source offers jwt.
func TestSources(t *testing.T) {
	want := []string{"alertmanager", "datadog", "dynatrace", "gcp", "generic", "grafana", "pagerduty", "webhook"}
	got := slices.Sorted(slices.Values(oncall.SourceIDs()))
	if !slices.Equal(got, want) {
		t.Fatalf("sources %v, want %v", got, want)
	}
	for _, s := range oncall.Sources {
		if len(s.AuthModes) == 0 || s.Label == "" {
			t.Errorf("%s: no auth mode or label", s.ID)
		}
		if slices.Contains(s.AuthModes, oncall.AuthJWT) != (s.ID == "webhook") {
			t.Errorf("%s offers jwt %v", s.ID, slices.Contains(s.AuthModes, oncall.AuthJWT))
		}
	}
	if _, ok := oncall.LookupSource("splunk"); ok {
		t.Error("an unknown source is in the catalog")
	}
}
