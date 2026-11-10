package domain

import (
	"errors"
	"testing"
)

// CheckText refuses a NUL and a string that is not UTF-8, in a key or a value at any depth, naming
// the field: Postgres refuses both in text (SQLSTATE 22021) where SQLite and memstore store them.
// JSON arguments are always UTF-8 (the decoder replaces a bad byte), but a percent-decoded URI path
// or query value is not.
func TestCheckText(t *testing.T) {
	for _, c := range []struct {
		name  string
		args  any
		field string // "" = accepted
	}{
		{"plain", map[string]any{"taskId": "T-1", "n": 3.0, "tags": []any{"a", "ü", "日本"}}, ""},
		{"replacement character", map[string]any{"title": "a�b"}, ""},
		{"NUL value", map[string]any{"taskId": "T\x00"}, "taskId"},
		{"NUL key", map[string]any{"a\x00": "x"}, "a\x00"},
		{"bad byte", map[string]any{"taskId": "T\xff"}, "taskId"},
		{"cut sequence", map[string]any{"cursor": "c\xc3"}, "cursor"},
		{"surrogate half", map[string]any{"q": "\xed\xa0\x80"}, "q"},
		{"bad key", map[string]any{"k\xff": "x"}, "k\xff"},
		{"nested", map[string]any{"a": map[string]any{"b": []any{"ok", "x\xfe"}}}, "a.b[1]"},
		{"bare string", "\xff", "(arguments)"},
	} {
		err := CheckText(c.args)
		if c.field == "" {
			if err != nil {
				t.Errorf("%s: CheckText = %v, want accepted", c.name, err)
			}
			continue
		}
		var e *BatonDeckError
		if !errors.As(err, &e) || e.Code != CodeValidation || e.Details["field"] != c.field {
			t.Errorf("%s: CheckText = %#v, want VALIDATION naming field %q", c.name, err, c.field)
		}
	}
}
