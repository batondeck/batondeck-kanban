package domain

import (
	"strconv"
	"strings"
)

// CheckText refuses a NUL character (U+0000) anywhere in decoded JSON arguments, in a key or a
// string value at any depth: VALIDATION naming the field's path. Postgres cannot store NUL in text
// and SQLite can, so without this one check the engines disagree (G3: INTERNAL on Postgres only).
// The transports call it on every tool's and route's arguments before the service sees them.
func CheckText(args any) error {
	return checkText("", args)
}

func checkText(path string, v any) error {
	switch x := v.(type) {
	case string:
		if strings.ContainsRune(x, 0) {
			field := path
			if field == "" {
				field = "(arguments)"
			}
			return Validation(field+" must not contain the NUL character (U+0000)", map[string]any{"field": field})
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
