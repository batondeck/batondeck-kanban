// Package blob is the local blob store (plan §Architecture "Blobs"): the files under one directory,
// named by their content's SHA-256 (store.go), and the HMAC signing of the URLs that upload and
// download them (sign.go). It holds no rows and no routes: the blob table is internal/store's, the
// PUT and GET /blobs/{id} routes internal/api's.
package blob

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SigningLabel is the keyring label the signing key is derived under (core.Keyring.DeriveKey).
const SigningLabel = "batondeck-kanban/blob-url-signing/v1"

// Purpose is what one signed URL authorises: an upload or a download, never the other.
type Purpose string

const (
	Upload   Purpose = "upload"
	Download Purpose = "download"
)

// The refusals of Verify. A signature for another purpose, id or expiry is ErrBadSignature.
var (
	ErrBadSignature = errors.New("blob: the signature does not match")
	ErrExpired      = errors.New("blob: the signature has expired")
)

// Signer signs and verifies (id, purpose, expiry) with HMAC-SHA256.
type Signer struct{ key []byte }

// NewSigner is a signer over key, which must be at least 32 bytes.
func NewSigner(key []byte) (*Signer, error) {
	if len(key) < 32 {
		return nil, errors.New("blob: a signing key is at least 32 bytes")
	}
	return &Signer{key: append([]byte(nil), key...)}, nil
}

// mac is the HMAC of purpose, expiry (Unix seconds) and id, newline-separated with id last, so no
// two triples share a message (neither a purpose nor a decimal holds a newline).
func (s *Signer) mac(id string, p Purpose, exp int64) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(string(p) + "\n" + strconv.FormatInt(exp, 10) + "\n" + id))
	return m.Sum(nil)
}

// Sign is the hex signature of id for p until exp.
func (s *Signer) Sign(id string, p Purpose, exp time.Time) string {
	return hex.EncodeToString(s.mac(id, p, exp.Unix()))
}

// URL is base/blobs/{id}?exp=…&sig=…, signed for p until exp.
func (s *Signer) URL(base, id string, p Purpose, exp time.Time) string {
	q := url.Values{"exp": {strconv.FormatInt(exp.Unix(), 10)}, "sig": {s.Sign(id, p, exp)}}
	return strings.TrimRight(base, "/") + "/blobs/" + url.PathEscape(id) + "?" + q.Encode()
}

// Verify checks sig over (id, p, exp) and then that now is before exp: ErrBadSignature for a
// signature that does not match (another purpose, id or expiry, or no signature), ErrExpired for a
// matching one past its expiry.
func (s *Signer) Verify(id string, p Purpose, exp, sig string, now time.Time) error {
	e, err := strconv.ParseInt(exp, 10, 64)
	if err != nil {
		return ErrBadSignature
	}
	got, err := hex.DecodeString(sig)
	if err != nil || !hmac.Equal(got, s.mac(id, p, e)) {
		return ErrBadSignature
	}
	if now.Unix() >= e {
		return ErrExpired
	}
	return nil
}

// Ref is the attachment an upload URL names: its (project, task, attachment) key, written as one
// path segment with dots between (no id of the three holds a dot).
type Ref struct {
	ProjectID    string
	TaskID       string
	AttachmentID string
}

func (r Ref) String() string { return r.ProjectID + "." + r.TaskID + "." + r.AttachmentID }

// ParseRef reads a Ref's String; ok is false for anything else.
func ParseRef(s string) (Ref, bool) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return Ref{}, false
	}
	return Ref{ProjectID: parts[0], TaskID: parts[1], AttachmentID: parts[2]}, true
}
