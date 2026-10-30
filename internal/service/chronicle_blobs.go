package service

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/blob"
	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// A Chronicle block may embed an attachment's download URL (list_attachments signs it for
// BlobURLTTL), and a stored URL would be dead a quarter of an hour later. So a write stores such a
// URL in one stable form and every read signs that form afresh (P-244), but only for a URL the
// server could still vouch for: one live when written, or one whose blob the page already held. An
// expired URL the page did not hold is stored as written and stays dead, so a writer cannot renew a
// URL it kept after losing access to the blob's project. A forged or foreign URL is never touched.

// blobURLRE is a signed blob URL as blob.Signer.URL writes it, with its base (public_url, the
// request's scheme://host, or none) or path-only: the blob id, the expiry and the signature.
var blobURLRE = regexp.MustCompile(`(?:https?://[^\s()<>"'\[\]]*?)?/blobs/([0-9a-f]{64})\?exp=([0-9]{1,19})&sig=([0-9a-f]{64})`)

// stableBlobExpiry is the expiry the stored form carries: the epoch, so it never fetches anything
// and is the same bytes whenever a re-signed URL is written back.
var stableBlobExpiry = time.Unix(0, 0)

// signedBlobURL is one download URL this server signed: its blob id, its expiry in Unix seconds
// (0 for the stored form) and whether it is still live.
type signedBlobURL struct {
	id   string
	exp  int64
	live bool
}

// rewriteBlobURLs is text with each download URL this server signed replaced by to(it), where to
// says so; every other URL, a forged or foreign one included, is left as it is. With no signer it is
// text.
func (c *Core) rewriteBlobURLs(text string, to func(u signedBlobURL) (string, bool)) string {
	s := c.media.Signer
	if s == nil || !strings.Contains(text, "/blobs/") {
		return text
	}
	now := c.now()
	return blobURLRE.ReplaceAllStringFunc(text, func(raw string) string {
		m := blobURLRE.FindStringSubmatch(raw)
		err := s.Verify(m[1], blob.Download, m[2], m[3], now)
		if err != nil && !errors.Is(err, blob.ErrExpired) {
			return raw
		}
		exp, perr := strconv.ParseInt(m[2], 10, 64)
		if perr != nil {
			return raw
		}
		if out, ok := to(signedBlobURL{id: m[1], exp: exp, live: err == nil}); ok {
			return out
		}
		return raw
	})
}

// revivedOnRead is whether a read signs u afresh in a version written at writtenAtMs: the stored
// form, or a URL that was live when written (a row from before P-244). A URL stored after P-244 in
// any other form was expired when written, and stays so.
func revivedOnRead(u signedBlobURL, writtenAtMs int64) bool {
	return u.exp == 0 || u.exp > writtenAtMs/1000
}

// stableURL is id's download URL in the stored form: path-only at stableBlobExpiry.
func (c *Core) stableURL(id string) string {
	return c.media.Signer.URL("", id, blob.Download, stableBlobExpiry)
}

// restoreBlobs is blocks with each block's text run through to, copied only where it changed. A
// block whose origin hashed its text before still does: the stored form is the same URL, not an
// edit.
func (c *Core) restoreBlobs(blocks []domain.ChronicleBlock, to func(u signedBlobURL) (string, bool)) []domain.ChronicleBlock {
	if c.media.Signer == nil {
		return blocks
	}
	var out []domain.ChronicleBlock
	for i, b := range blocks {
		text := c.rewriteBlobURLs(b.Text, to)
		if text == b.Text {
			continue
		}
		if out == nil {
			out = slices.Clone(blocks)
		}
		nb := &out[i]
		if b.Origin != nil && b.Origin.BlockHash == domain.BlockHash(b.Text) {
			o := *b.Origin
			o.BlockHash = domain.BlockHash(text)
			nb.Origin = &o
		}
		nb.Text = text
	}
	if out == nil {
		return blocks
	}
	return out
}

// liveBlobURLs is the first step of every write: each live signed blob URL in the stored form.
// Expired ones wait for heldBlobURLs, which knows what the page held; sent is the blob ids the
// writer sent in the stored form itself, for heldBlobURLs to check.
func (c *Core) liveBlobURLs(blocks []domain.ChronicleBlock) (out []domain.ChronicleBlock, sent []string) {
	out = c.restoreBlobs(blocks, func(u signedBlobURL) (string, bool) {
		if u.exp == 0 {
			sent = append(sent, u.id)
		}
		if !u.live {
			return "", false
		}
		return c.stableURL(u.id), true
	})
	return out, sent
}

// heldBlobURLs is the second step, once the write has read the page: an expired signed blob URL
// whose blob the page held (heldBlobIDs) is put in the stored form; any other expired URL is
// stored as written, dead. A blob sent in the stored form itself (liveBlobURLs) is VALIDATION
// unless the page held it: no read returns that form, so a writer who has one did not get it from
// this server.
func (c *Core) heldBlobURLs(blocks []domain.ChronicleBlock, sent []string, held map[string]bool) ([]domain.ChronicleBlock, error) {
	for _, id := range sent {
		if !held[id] {
			return nil, domain.Validation("a block holds a blob URL in the stored form; paste the attachment's download URL", map[string]any{"field": "blocks"})
		}
	}
	return c.restoreBlobs(blocks, func(u signedBlobURL) (string, bool) {
		if u.live || u.exp == 0 || !held[u.id] {
			return "", false
		}
		return c.stableURL(u.id), true
	}), nil
}

// heldBlobIDs adds to held each blob a version written at writtenAtMs holds: one whose URL a read
// of it signs afresh (revivedOnRead).
func (c *Core) heldBlobIDs(held map[string]bool, blocks []domain.ChronicleBlock, writtenAtMs int64) {
	for _, b := range blocks {
		c.rewriteBlobURLs(b.Text, func(u signedBlobURL) (string, bool) {
			if revivedOnRead(u, writtenAtMs) {
				held[u.id] = true
			}
			return "", false
		})
	}
}

// freshBlobURLs is blocks of a version written at writtenAtMs, copied, with each signed blob URL a
// read revives (revivedOnRead) signed for download at BlobBase(ctx), valid BlobURLTTL from now:
// what a read returns, never stored. Any other URL is returned as stored. The store's slice is not
// written.
func (c *Core) freshBlobURLs(ctx context.Context, blocks []domain.ChronicleBlock, writtenAtMs int64) []domain.ChronicleBlock {
	if c.media.Signer == nil {
		return blocks
	}
	exp := c.signedExpiry()
	out := slices.Clone(blocks)
	for i := range out {
		out[i].Text = c.rewriteBlobURLs(out[i].Text, func(u signedBlobURL) (string, bool) {
			if !revivedOnRead(u, writtenAtMs) {
				return "", false
			}
			return c.SignBlobURL(ctx, u.id, blob.Download, exp), true
		})
	}
	return out
}
