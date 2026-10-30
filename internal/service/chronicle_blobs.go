package service

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/blob"
	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// A Chronicle block may embed an attachment's download URL (list_attachments signs it for
// BlobURLTTL), and a stored URL would be dead a quarter of an hour later. So a write stores each
// such URL in one stable form and every read signs it afresh (P-244). Only a URL this server
// signed is touched, expired or not: a read never signs an id a writer merely named.

// blobURLRE is a signed blob URL as blob.Signer.URL writes it, with its base (public_url, the
// request's scheme://host, or none) or path-only: the blob id, the expiry and the signature.
var blobURLRE = regexp.MustCompile(`(?:https?://[^\s()<>"'\[\]]*?)?/blobs/([0-9a-f]{64})\?exp=([0-9]{1,19})&sig=([0-9a-f]{64})`)

// stableBlobExpiry is the expiry the stored form carries: the epoch, so it never fetches anything
// and is the same bytes whenever a re-signed URL is written back.
var stableBlobExpiry = time.Unix(0, 0)

// rewriteBlobURLs is text with each download URL this server signed replaced by to(its blob id);
// every other URL, a forged or foreign one included, is left as it is. With no signer it is text.
func (c *Core) rewriteBlobURLs(text string, to func(id string) string) string {
	s := c.media.Signer
	if s == nil || !strings.Contains(text, "/blobs/") {
		return text
	}
	now := c.now()
	return blobURLRE.ReplaceAllStringFunc(text, func(u string) string {
		m := blobURLRE.FindStringSubmatch(u)
		if err := s.Verify(m[1], blob.Download, m[2], m[3], now); err != nil && !errors.Is(err, blob.ErrExpired) {
			return u
		}
		return to(m[1])
	})
}

// stableBlobURLs puts each block's signed blob URLs in the stored form, path-only at
// stableBlobExpiry. A block whose origin hashed its text before still does: the stored form is
// the same URL, not an edit.
func (c *Core) stableBlobURLs(blocks []domain.ChronicleBlock) {
	if c.media.Signer == nil {
		return
	}
	for i := range blocks {
		b := &blocks[i]
		text := c.rewriteBlobURLs(b.Text, func(id string) string {
			return c.media.Signer.URL("", id, blob.Download, stableBlobExpiry)
		})
		if text == b.Text {
			continue
		}
		if b.Origin != nil && b.Origin.BlockHash == domain.BlockHash(b.Text) {
			b.Origin.BlockHash = domain.BlockHash(text)
		}
		b.Text = text
	}
}

// freshBlobURLs is blocks, copied, with each signed blob URL signed for download at
// BlobBase(ctx), valid BlobURLTTL from now: what a read returns, never stored. The store's slice
// is not written.
func (c *Core) freshBlobURLs(ctx context.Context, blocks []domain.ChronicleBlock) []domain.ChronicleBlock {
	if c.media.Signer == nil {
		return blocks
	}
	exp := c.signedExpiry()
	out := slices.Clone(blocks)
	for i := range out {
		out[i].Text = c.rewriteBlobURLs(out[i].Text, func(id string) string {
			return c.SignBlobURL(ctx, id, blob.Download, exp)
		})
	}
	return out
}
