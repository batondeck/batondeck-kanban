package domain

import (
	"math/rand/v2"
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

// OrderBetweenSpread keys sort strictly between their bounds, are well formed, and differ by
// entropy, including where OrderBetween's key is a prefix of the upper bound ("a0" for ("", "a0V")).
func TestOrderBetweenSpread(t *testing.T) {
	gaps := [][2]string{{"", ""}, {"", "a0"}, {"a0", ""}, {"a0", "a1"}, {"", "a0V"}, {"a0", "a0V"}, {"a0", "a01"},
		{"a0", "a0001"}, {"Zz", "a0"}, {"a1", "a0"}, {"", "A00000000000000000000000001"}, {"a0V", "a0Vz"}}
	for _, g := range gaps {
		lo, hi := g[0], g[1]
		if lo > hi && hi != "" {
			lo, hi = hi, lo
		}
		seen := map[string]bool{}
		for i := range 2000 {
			k, err := OrderBetweenSpread(g[0], g[1], uint32(i)*2654435761)
			if err != nil {
				t.Fatalf("OrderBetweenSpread(%q, %q): %v", g[0], g[1], err)
			}
			if !(lo == "" || lo < k) || !(hi == "" || k < hi) || !IsValidOrder(k) {
				t.Fatalf("OrderBetweenSpread(%q, %q) = %q: not a well-formed key strictly between", g[0], g[1], k)
			}
			seen[k] = true
		}
		if len(seen) < 1900 {
			t.Errorf("OrderBetweenSpread(%q, %q): %d distinct keys of 2000", g[0], g[1], len(seen))
		}
	}
	if _, err := OrderBetweenSpread("a0", "a0", 7); err == nil {
		t.Fatal("OrderBetweenSpread(a0, a0) has no gap and must fail")
	}
}

// A head whose fraction runs through low digits ("a01111V", "Zn111eAwM", which repeated head
// placements produce on their own) leaves no room after OrderBetween's key for a spread; writers that
// read that head at once must still mint different keys, not the bare key each.
func TestOrderBetweenSpreadBeforeACrowdedHead(t *testing.T) {
	for _, g := range [][2]string{{"", "a01111V"}, {"", "Zn111eAwM"}, {"", "a0001"}, {"", "Zz00011"},
		{"a0", "a01111V"}, {"Zn1", "Zn111eAwM"}, {"a00001", "a0001"}} {
		bare := mustOrder(t, g[0], g[1])
		seen := map[string]bool{}
		for i := range 1000 {
			k, err := OrderBetweenSpread(g[0], g[1], uint32(i)*2654435761)
			if err != nil {
				t.Fatalf("OrderBetweenSpread(%q, %q): %v", g[0], g[1], err)
			}
			if !(g[0] == "" || g[0] < k) || !(k < g[1]) || !IsValidOrder(k) {
				t.Fatalf("OrderBetweenSpread(%q, %q) = %q: not a well-formed key strictly between", g[0], g[1], k)
			}
			if k == bare {
				t.Fatalf("OrderBetweenSpread(%q, %q, entropy #%d) = %q, OrderBetween's own key with no spread", g[0], g[1], i, k)
			}
			seen[k] = true
		}
		if len(seen) < 990 {
			t.Errorf("OrderBetweenSpread(%q, %q): %d distinct keys of 1000", g[0], g[1], len(seen))
		}
	}
}

// Placing task after task at a column's head (each before the last) always spreads the key and keeps
// keys short: no placement falls back to OrderBetween's bare key, which two writers reading the same
// head would both mint.
func TestOrderBetweenSpreadChainedHeadPlacements(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	head, longest := "a0", 0
	for i := range 10_000 {
		k, err := OrderBetweenSpread("", head, r.Uint32())
		if err != nil {
			t.Fatalf("placement %d before %q: %v", i, head, err)
		}
		if !(k < head) || !IsValidOrder(k) {
			t.Fatalf("placement %d before %q = %q: not a well-formed key before it", i, head, k)
		}
		if bare := mustOrder(t, "", head); k == bare {
			t.Fatalf("placement %d before %q = %q, OrderBetween's own key with no spread", i, head, k)
		}
		longest = max(longest, len(k))
		head = k
	}
	if longest > 16 {
		t.Errorf("10,000 head placements reached a %d-character key, want at most 16", longest)
	}
}
