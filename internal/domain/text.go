package domain

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// CheckText refuses a NUL character (U+0000), or a string that is not valid UTF-8, anywhere in
// decoded arguments, in a key or a string value at any depth: VALIDATION naming the field's path.
// Postgres cannot store either in text and SQLite can, so without this one check the engines
// disagree (G3: INTERNAL on Postgres only). JSON arguments are always UTF-8 (the decoder replaces
// a bad byte), but a value percent-decoded from a URL path or query, or from an MCP resource URI,
// can carry any byte. The transports call it on every tool's, route's and resource read's
// arguments before the service sees them.
func CheckText(args any) error {
	return checkText("", args)
}

func checkText(path string, v any) error {
	switch x := v.(type) {
	case string:
		field := path
		if field == "" {
			field = "(arguments)"
		}
		if strings.ContainsRune(x, 0) {
			return Validation(field+" must not contain the NUL character (U+0000)", map[string]any{"field": field})
		}
		if !utf8.ValidString(x) {
			return Validation(field+" must be valid UTF-8", map[string]any{"field": field})
		}
	case map[string]any:
		for k, c := range x {
			p := k
			if path != "" {
				p = path + "." + k
			}
			if err := checkText(p, k); err != nil {
				return err
			}
			if err := checkText(p, c); err != nil {
				return err
			}
		}
	case []any:
		for i, c := range x {
			if err := checkText(path+"["+strconv.Itoa(i)+"]", c); err != nil {
				return err
			}
		}
	}
	return nil
}
