package sliceutil

import "testing"

func TestContains(t *testing.T) {
	if !Contains([]string{"a", "b", "c"}, "b") {
		t.Error("expected true for present element")
	}
	if Contains([]string{"a", "b"}, "x") {
		t.Error("expected false for absent element")
	}
	if Contains[int](nil, 1) {
		t.Error("expected false on nil slice")
	}
}

func TestDedupe_PreservesFirstSeenOrder(t *testing.T) {
	got := Dedupe([]string{"b", "a", "b", "c", "a"})
	want := []string{"b", "a", "c"}
	if len(got) != len(want) {
		t.Fatalf("len %d, want %d: got=%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDedupe_DropsZeroValue(t *testing.T) {
	got := Dedupe([]string{"", "a", "", "b"})
	want := []string{"a", "b"}
	if len(got) != len(want) || got[0] != "a" || got[1] != "b" {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b []string
		want bool
	}{
		{"empty equal", nil, nil, true},
		{"different lengths", []string{"a"}, []string{"a", "b"}, false},
		{"same set different order", []string{"a", "b", "c"}, []string{"c", "a", "b"}, true},
		{"duplicates count", []string{"a", "a", "b"}, []string{"a", "b", "b"}, false},
		{"disjoint", []string{"a", "b"}, []string{"c", "d"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Equal(tt.a, tt.b); got != tt.want {
				t.Errorf("Equal(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
