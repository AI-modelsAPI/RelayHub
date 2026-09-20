package affinity

import "testing"

func TestRememberLookupAndExpiry(t *testing.T) {
	tab := New(0)
	tab.Remember("s", "chan-a", "key-1")
	ch, key, ok := tab.Lookup("s")
	if !ok || ch != "chan-a" || key != "key-1" {
		t.Fatalf("got %s %s %v", ch, key, ok)
	}
	tab.Forget("s")
	if _, _, ok := tab.Lookup("s"); ok {
		t.Fatal("expected miss after forget")
	}
}
