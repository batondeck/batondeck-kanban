package domain

import (
	"errors"
	"strings"
)

// Order keys: fractional indexing, so a task can always be placed between two others without
// rewriting its neighbours. This is a port of generateKeyBetween from the npm package
// fractional-indexing 4.0.0 (CC0 1.0, no rights reserved; https://github.com/rocicorp/fractional-indexing),
// the library conductor's src/util/lexorank.ts wraps, with its default alphabets: base-62 digits and
// A-Z/a-z integer heads, so keys read "a0", "a1", "Zz", … exactly as conductor writes them.

const (
	orderDigits    = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	orderIntDigits = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

var (
	digitIndex    = indexOf(orderDigits)
	intDigitIndex = indexOf(orderIntDigits)
	// The smallest integer: the most negative head followed by all-zero digits. It is not a key.
	smallestInteger = orderIntDigits[:1] + strings.Repeat(orderDigits[:1], len(orderIntDigits)/2)
)

// indexOf maps each byte to its position in alphabet; a byte outside it maps to 0, as the library's
// lookup table does.
func indexOf(alphabet string) [256]int {
	var m [256]int
	for i := 0; i < len(alphabet); i++ {
		m[alphabet[i]] = i
	}
	return m
}

// OrderBetween returns a key that sorts strictly between a and b. "" stands for an open end: ("", "")
// is the first key, (a, "") a key after a, ("", b) a key before b. Two keys given out of order are
// swapped, as the library does. A malformed key, or a == b, is an error.
func OrderBetween(a, b string) (string, error) {
	hasA, hasB := a != "", b != ""
	if hasA {
		if err := validateOrderKey(a); err != nil {
			return "", err
		}
	}
	if hasB {
		if err := validateOrderKey(b); err != nil {
			return "", err
		}
	}
	if hasA && hasB && a > b {
		a, b = b, a
	}

	if !hasA {
		if !hasB {
			return orderIntDigits[len(orderIntDigits)/2:len(orderIntDigits)/2+1] + orderDigits[:1], nil
		}
		ib, err := integerPart(b)
		if err != nil {
			return "", err
		}
		fb := b[len(ib):]
		if ib == smallestInteger {
			m, err := midpoint("", fb, true)
			if err != nil {
				return "", err
			}
			return ib + m, nil
		}
		if ib < b {
			return ib, nil
		}
		res, ok, err := decrementInteger(ib)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", errors.New("order key: cannot decrement any more")
		}
		return res, nil
	}

	ia, err := integerPart(a)
	if err != nil {
		return "", err
	}
	fa := a[len(ia):]
	if !hasB {
		i, ok, err := incrementInteger(ia)
		if err != nil {
			return "", err
		}
		if ok {
			return i, nil
		}
		m, err := midpoint(fa, "", false)
		if err != nil {
			return "", err
		}
		return ia + m, nil
	}

	ib, err := integerPart(b)
	if err != nil {
		return "", err
	}
	fb := b[len(ib):]
	if ia == ib {
		m, err := midpoint(fa, fb, true)
		if err != nil {
			return "", err
		}
		return ia + m, nil
	}
	i, ok, err := incrementInteger(ia)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("order key: cannot increment any more")
	}
	if i < b {
		return i, nil
	}
	m, err := midpoint(fa, "", false)
	if err != nil {
		return "", err
	}
	return ia + m, nil
}

// OrderBetweenSpread is OrderBetween with four more digits drawn from entropy, so two writers that
// place a task in the same gap from the same read (each before the other's write is visible) mint
// different keys rather than one key twice. The key still sorts strictly between a and b; where no
// spread key fits the gap it is OrderBetween's own.
func OrderBetweenSpread(a, b string, entropy uint32) (string, error) {
	if a != "" && b != "" && a > b {
		a, b = b, a
	}
	k, err := OrderBetween(a, b)
	if err != nil {
		return "", err
	}
	var spread [4]byte // three digits and a last one that is not zero, so the key stays well formed
	for i := range 3 {
		spread[i] = orderDigits[entropy%62]
		entropy /= 62
	}
	spread[3] = orderDigits[1+entropy%61]
	// k + spread sorts after k, so after a; it sorts before b unless k is a prefix of b, and then a key
	// nearer b is tried.
	lo := k
	for range 4 {
		if c := lo + string(spread[:]); b == "" || c < b {
			return c, nil
		}
		if lo, err = OrderBetween(lo, b); err != nil {
			break
		}
	}
	return k, nil
}

// MaxOrderKeyLen is the longest order key a caller may send: keys squeezed into one gap grow about
// one digit per six placements, so it allows some 1,500 placements into the same gap.
const MaxOrderKeyLen = 256

// IsValidOrder reports whether key is a well-formed order key a caller may store: at most
// MaxOrderKeyLen bytes, every one of the alphabet (an A-Z/a-z head, base-62 digits), and well formed.
// Validate a caller's key with it before storing: a malformed stored key makes every later
// OrderBetween against it fail, and a byte outside the alphabet (read as the zero digit, as the
// library does) makes later keys computed against it land on the wrong side of it.
func IsValidOrder(key string) bool {
	if key == "" || len(key) > MaxOrderKeyLen {
		return false
	}
	for i := 1; i < len(key); i++ {
		if orderDigits[digitIndex[key[i]]] != key[i] {
			return false
		}
	}
	_, err := OrderBetween(key, "")
	return err == nil
}

// midpoint returns a fraction strictly between a and b (b absent when !hasB). Neither may end in the
// zero digit.
func midpoint(a, b string, hasB bool) (string, error) {
	zero := orderDigits[0]
	if hasB && a >= b {
		return "", errors.New("order key: " + a + " >= " + b)
	}
	if (a != "" && a[len(a)-1] == zero) || (hasB && b != "" && b[len(b)-1] == zero) {
		return "", errors.New("order key: trailing zero")
	}
	if hasB && b != "" {
		// Strip the longest common prefix, padding a with zeros.
		n := 0
		for n < len(b) && byteAt(a, n, zero) == b[n] {
			n++
		}
		if n > 0 {
			m, err := midpoint(a[min(n, len(a)):], b[n:], true)
			if err != nil {
				return "", err
			}
			return b[:n] + m, nil
		}
	}
	digitA := 0
	if a != "" {
		digitA = digitIndex[a[0]]
	}
	digitB := len(orderDigits)
	if hasB {
		digitB = digitIndex[b[0]]
	}
	if digitB-digitA > 1 {
		return orderDigits[(digitA+digitB+1)/2 : (digitA+digitB+1)/2+1], nil
	}
	if hasB && len(b) > 1 {
		return b[:1], nil
	}
	rest := ""
	if a != "" {
		rest = a[1:]
	}
	m, err := midpoint(rest, "", false)
	if err != nil {
		return "", err
	}
	return orderDigits[digitA:digitA+1] + m, nil
}

func byteAt(s string, i int, pad byte) byte {
	if i < len(s) {
		return s[i]
	}
	return pad
}

func integerLength(head byte) (int, error) {
	i := intDigitIndex[head]
	if orderIntDigits[i] != head {
		return 0, errors.New("order key: invalid head " + string(head))
	}
	half := len(orderIntDigits) / 2
	if i < half {
		return half - i + 1, nil
	}
	return i - half + 2, nil
}

func integerPart(key string) (string, error) {
	if key == "" {
		return "", errors.New("order key: empty")
	}
	n, err := integerLength(key[0])
	if err != nil {
		return "", err
	}
	if n > len(key) {
		return "", errors.New("order key: invalid " + key)
	}
	return key[:n], nil
}

func validateOrderKey(key string) error {
	if key == smallestInteger {
		return errors.New("order key: invalid " + key)
	}
	i, err := integerPart(key)
	if err != nil {
		return err
	}
	if f := key[len(i):]; f != "" && f[len(f)-1] == orderDigits[0] {
		return errors.New("order key: invalid " + key)
	}
	return nil
}

func validateInteger(x string) error {
	n, err := integerLength(x[0])
	if err != nil {
		return err
	}
	if len(x) != n {
		return errors.New("order key: invalid integer part " + x)
	}
	return nil
}

// incrementInteger returns the next integer part, or ok=false past the largest one.
func incrementInteger(x string) (string, bool, error) {
	if err := validateInteger(x); err != nil {
		return "", false, err
	}
	head := x[0]
	trailing := ""
	for i := len(x) - 1; i >= 1; i-- {
		d := digitIndex[x[i]] + 1
		if d == len(orderDigits) {
			trailing = orderDigits[:1] + trailing
		} else {
			return x[:i] + orderDigits[d:d+1] + trailing, true, nil
		}
	}
	hi := intDigitIndex[head]
	if hi == len(orderIntDigits)-1 {
		return "", false, nil
	}
	h := orderIntDigits[hi+1]
	return string(h) + resize(trailing, h, head, orderDigits[0]), true, nil
}

// decrementInteger returns the previous integer part, or ok=false below the smallest one.
func decrementInteger(x string) (string, bool, error) {
	if err := validateInteger(x); err != nil {
		return "", false, err
	}
	head := x[0]
	last := orderDigits[len(orderDigits)-1]
	trailing := ""
	for i := len(x) - 1; i >= 1; i-- {
		d := digitIndex[x[i]] - 1
		if d == -1 {
			trailing = string(last) + trailing
		} else {
			return x[:i] + orderDigits[d:d+1] + trailing, true, nil
		}
	}
	hi := intDigitIndex[head]
	if hi == 0 {
		return "", false, nil
	}
	h := orderIntDigits[hi-1]
	return string(h) + resize(trailing, h, head, last), true, nil
}

// resize grows or shrinks a carried digit run by one to match the new head's integer length.
func resize(trailing string, newHead, oldHead, fill byte) string {
	nl, _ := integerLength(newHead)
	ol, _ := integerLength(oldHead)
	switch {
	case nl > ol:
		return trailing + string(fill)
	case nl < ol:
		return trailing[1:]
	default:
		return trailing
	}
}
