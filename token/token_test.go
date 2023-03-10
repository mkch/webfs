package token

import (
	"testing"
)

func TestLength(t *testing.T) {
	if l := len(New(0)); l != 0 {
		t.Fatal(0)
	}

	for n := 0; n < 100; n++ {
		if l := len(New(n)); l != n {
			t.Fatal(n)
		}
	}
}

func TestUniqueness(t *testing.T) {
	const L = 6
	const N = 1000
	tokens := make(map[string]struct{})
	for i := 0; i < N; i++ {
		tokens[New(L)] = struct{}{}
	}
	if N-len(tokens) > 2 {
		t.Fatal("uniqueness")
	}
}
