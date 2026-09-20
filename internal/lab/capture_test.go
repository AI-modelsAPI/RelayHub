package lab

import "testing"

func TestRingDisabledByDefault(t *testing.T) {
	r := NewRing(2)
	r.Push(Capture{Model: "m", Body: []byte("x")})
	if len(r.List()) != 0 {
		t.Fatal("disabled should drop")
	}
	r.SetEnabled(true)
	r.Push(Capture{Model: "m", Body: []byte("x")})
	if len(r.List()) != 1 {
		t.Fatal("enabled should keep")
	}
	if _, ok := r.Get("cap-1"); !ok {
		t.Fatal("missing id")
	}
}
