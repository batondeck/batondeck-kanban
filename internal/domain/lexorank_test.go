package domain

import (
	"strings"
	"testing"
)

func mustOrder(t *testing.T, a, b string) string {
	t.Helper()
	k, err := OrderBetween(a, b)
	if err != nil {
		t.Fatalf("OrderBetween(%q, %q): %v", a, b, err)
	}
	return k
}

func TestLexorank(t *testing.T) {
	t.Run("produces strictly increasing keys", func(t *testing.T) {
		a := mustOrder(t, "", "")
		b := mustOrder(t, a, "")
		mid := mustOrder(t, a, b)
		if !(a < b && a < mid && mid < b) {
			t.Fatalf("a=%q mid=%q b=%q", a, mid, b)
		}
	})
	t.Run("can always insert between two adjacent keys", func(t *testing.T) {
		lo := mustOrder(t, "", "")
		hi := mustOrder(t, lo, "")
		for i := 0; i < 50; i++ {
			mid := mustOrder(t, lo, hi)
			if !(lo < mid && mid < hi) {
				t.Fatalf("step %d: %q < %q < %q fails", i, lo, mid, hi)
			}
			hi = mid
		}
	})
	t.Run("isValidOrder accepts well-formed keys and rejects malformed ones", func(t *testing.T) {
		if !IsValidOrder(mustOrder(t, "", "")) || !IsValidOrder(mustOrder(t, "a0", "")) {
			t.Fatal("a generated key is invalid")
		}
		for _, bad := range []string{"z", "", " ", "!!", "not-a-key"} {
			if IsValidOrder(bad) {
				t.Fatalf("IsValidOrder(%q) = true", bad)
			}
		}
	})
}

// TestLexorankMatchesReference holds the port to fractional-indexing 4.0.0's generateKeyBetween. Each
// expected value was produced by that library as conductor installs it (node_modules), with "" for null
// and "ERR" for a throw.
func TestLexorankMatchesReference(t *testing.T) {
	for _, c := range []struct{ a, b, want string }{
		{"", "", "a0"},
		{"a0", "", "a1"},
		{"", "a0", "Zz"},
		{"a0", "a1", "a0V"},
		{"a1", "a0", "a0V"},
		{"a0", "a0V", "a0G"},
		{"a0V", "a1", "a0l"},
		{"a09", "a1", "a0a"},
		{"az", "", "b00"},
		{"azz", "", "b00"},
		{"Zz", "", "a0"},
		{"", "Zz", "Zy"},
		{"", "b00", "az"},
		{"b00", "", "b01"},
		{"a0", "a0001", "a0000V"},
		{"a1", "a2", "a1V"},
		{"a1", "b00", "a2"},
		{"Zy", "a0", "Zz"},
		{"zzzzzzzzzzzzzzzzzzzzzzzzzzz", "", "zzzzzzzzzzzzzzzzzzzzzzzzzzzV"},
		{"", "A00000000000000000000000001", "A00000000000000000000000000"},
		{"A00000000000000000000000001", "", "A00000000000000000000000002"},
		{"a0!", "", "a1"},
		{"a0V", "a0Vz", "a0VV"},
		{"a0Vz", "a0W", "a0VzV"},
		{"a0", "a0", "ERR"},
		{"a00", "", "ERR"},
		{"A00000000000000000000000000", "", "ERR"},
	} {
		got, err := OrderBetween(c.a, c.b)
		if err != nil {
			got = "ERR"
		}
		if got != c.want {
			t.Errorf("OrderBetween(%q, %q) = %q (%v), want %q", c.a, c.b, got, err, c.want)
		}
	}
	lo := mustOrder(t, "", "")
	hi := mustOrder(t, lo, "")
	for i := 0; i < 50; i++ {
		hi = mustOrder(t, lo, hi)
	}
	if lo != "a0" || hi != "a000000000G" {
		t.Errorf("50 squeezes: lo=%q hi=%q, want a0 and a000000000G", lo, hi)
	}
	for key, want := range map[string]bool{
		"a0": true, "a0V": true, "Zz": true, "b00": true, "zzzzzzzzzzzzzzzzzzzzzzzzzzz": true,
		"a00": false, "a": false, "b0": false, "A00000000000000000000000000": false,
		// A caller's key holds only the alphabet (OrderBetween reads "a0!" as the library does, but
		// a stored "a0~" sorts on the wrong side of the keys computed against it) and is bounded.
		"a0!": false, "a0 ": false, "a0~": false, "a0é": false, "a!": false, "a0\x00": false, "a0<script>": false,
		"a0\n<b>": false, "a" + strings.Repeat("0", 254) + "V": true, "a" + strings.Repeat("0", 255) + "V": false,
		"a" + strings.Repeat("0", 200_000) + "V": false,
	} {
		if got := IsValidOrder(key); got != want {
			t.Errorf("IsValidOrder(%.40q, %d bytes) = %v, want %v", key, len(key), got, want)
		}
	}
}
