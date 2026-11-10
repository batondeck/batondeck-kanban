package store

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// EncodeCursor is the page cursor every engine mints and reads, conductor's wire format
// (store-helpers.ts:31): base64url, unpadded, of the JSON {"order":order,"id":id} of a page's last
// row.
func EncodeCursor(order, id string) string {
	b, _ := json.Marshal(struct {
		Order string `json:"order"`
		ID    string `json:"id"`
	}{order, id})
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor reads a cursor EncodeCursor minted. Anything else, a well-formed JSON value of
// another shape included, is VALIDATION: the value is the caller's. A field holding U+0000, which
// Postgres cannot bind as text (so no Postgres row holds it), is refused alike on every engine rather
// than reaching a statement. Other control characters pass: order keys stored before move_task
// checked the key alphabet can hold them, and a cursor minted at such a task must read on.
func DecodeCursor(cursor string) (order, id string, err error) {
	invalid := domain.Validation("Invalid cursor", nil)
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", "", invalid
	}
	var c struct {
		Order *string `json:"order"`
		ID    *string `json:"id"`
	}
	if json.Unmarshal(raw, &c) != nil || c.Order == nil || c.ID == nil || strings.ContainsRune(*c.Order, 0) || strings.ContainsRune(*c.ID, 0) {
		return "", "", invalid
	}
	return *c.Order, *c.ID, nil
}
