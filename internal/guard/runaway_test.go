package guard

import "testing"

func TestTripOnIdenticalPayload(t *testing.T) {
	g := New()
	g.limit = 3
	body := []byte("same")
	if g.Trip("s", body) {
		t.Fatal("first should not trip")
	}
	if g.Trip("s", body) {
		t.Fatal("second should not trip")
	}
	if g.Trip("s", body) {
		t.Fatal("third should not trip (same==2)")
	}
	if !g.Trip("s", body) {
		t.Fatal("fourth should trip")
	}
}
