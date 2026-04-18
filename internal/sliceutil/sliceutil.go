// Package sliceutil provides small generic helpers for slices of comparable
// values — the patterns that kept getting re-implemented across CLI and UI
// code (membership test, first-seen dedupe, set equality).
package sliceutil

// Contains reports whether s contains v.
func Contains[T comparable](s []T, v T) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// Dedupe returns s with duplicates removed, preserving first-seen order.
// The zero value of T is dropped.
func Dedupe[T comparable](s []T) []T {
	var zero T
	seen := make(map[T]bool, len(s))
	var out []T
	for _, v := range s {
		if v == zero || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// Equal reports whether a and b contain the same elements, regardless of order
// and with duplicates counted. Useful for comparing "selection" slices.
func Equal[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[T]int, len(a))
	for _, v := range a {
		counts[v]++
	}
	for _, v := range b {
		counts[v]--
	}
	for _, c := range counts {
		if c != 0 {
			return false
		}
	}
	return true
}
