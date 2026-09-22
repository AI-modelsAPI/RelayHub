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

func TestRingClearKeepsEnabled(t *testing.T) {
	r := NewRing(4)
	r.SetEnabled(true)
	r.Push(Capture{Model: "m", Body: []byte("x")})
	r.Push(Capture{Model: "m", Body: []byte("y")})
	if len(r.List()) != 2 {
		t.Fatalf("expected 2 captures, got %d", len(r.List()))
	}
	r.Clear()
	if len(r.List()) != 0 {
		t.Fatal("clear should drop all captures")
	}
	if !r.Enabled() {
		t.Fatal("clear must not disable capture")
	}
	r.Push(Capture{Model: "m", Body: []byte("z")})
	if got := r.List(); len(got) != 1 || got[0].ID != "cap-3" {
		t.Fatalf("sequence must continue after clear, got %+v", got)
	}
	var nilRing *Ring
	nilRing.Clear() // must not panic
}

func TestRingTruncatesOversizedBody(t *testing.T) {
	r := NewRing(1)
	r.SetEnabled(true)
	big := make([]byte, maxCaptureBody+10)
	r.Push(Capture{Model: "m", Body: big})
	c, ok := r.Get("cap-1")
	if !ok {
		t.Fatal("missing capture")
	}
	if len(c.Body) != maxCaptureBody {
		t.Fatalf("body should be capped at %d, got %d", maxCaptureBody, len(c.Body))
	}
	if !c.Truncated || c.Size != len(big) {
		t.Fatalf("truncation must be reported with original size: truncated=%v size=%d", c.Truncated, c.Size)
	}
}
