package tlspin

import "testing"

func TestNormalize(t *testing.T) {
	if Normalize("AA:BB:CC") != "aabbcc" {
		t.Fatal("normalize")
	}
	if Normalize(" aa bb ") != "aabb" {
		t.Fatal("normalize spaces")
	}
}
