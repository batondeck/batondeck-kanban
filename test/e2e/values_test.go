//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// M is a decoded JSON object.
type M = map[string]any

// at walks v by a dotted path; a numeric segment indexes an array. A missing step is nil.
func at(v any, path string) any {
	if path == "" {
		return v
	}
	for _, seg := range strings.Split(path, ".") {
		switch x := v.(type) {
		case map[string]any:
			v = x[seg]
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(x) {
				return nil
			}
			v = x[i]
		default:
			return nil
		}
	}
	return v
}

// S is the string at path, "" when it is not one.
func S(v any, path string) string { s, _ := at(v, path).(string); return s }

// N is the number at path, -1 when it is not one.
func N(v any, path string) float64 {
	if n, ok := at(v, path).(float64); ok {
		return n
	}
	return -1
}

// A is the array at path, nil when it is not one.
func A(v any, path string) []any { a, _ := at(v, path).([]any); return a }

// O is the object at path, nil when it is not one.
func O(v any, path string) M { m, _ := at(v, path).(map[string]any); return m }

// B is the boolean at path.
func B(v any, path string) bool { b, _ := at(v, path).(bool); return b }

// js is v as compact JSON, for messages.
func js(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// ids is the field key of every object in list.
func ids(list []any, key string) []string {
	out := make([]string, 0, len(list))
	for _, it := range list {
		out = append(out, S(it, key))
	}
	return out
}

// has reports whether list holds s.
func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// strs is a JSON array of strings as a Go slice.
func strs(list []any) []string {
	out := make([]string, 0, len(list))
	for _, x := range list {
		out = append(out, fmt.Sprint(x))
	}
	return out
}

// containsStr reports whether s holds sub.
func containsStr(s, sub string) bool { return strings.Contains(s, sub) }

// want fails the test with msg and the value when ok is false.
func want(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(format, args...)
	}
}
