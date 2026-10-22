package store

import (
	"encoding/base64"
	"encoding/json"

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
// another shape included, is VALIDATION: the value is the caller's.
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
	if json.Unmarshal(raw, &c) != nil || c.Order == nil || c.ID == nil {
		return "", "", invalid
	}
	return *c.Order, *c.ID, nil
}
