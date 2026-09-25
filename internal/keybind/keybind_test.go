package keybind

import "testing"

func TestOriginNormalisation(t *testing.T) {
	cases := map[string]string{
		"https://Relay.Example.com/v1":   "https://relay.example.com:443",
		"https://relay.example.com:443/": "https://relay.example.com:443",
		"http://127.0.0.1:3000":          "http://127.0.0.1:3000",
		"http://[::1]/v1":                "http://[::1]:80",
		"ftp://x":                        "",
		"not a url":                      "",
	}
	for in, want := range cases {
		if got := Origin(in); got != want {
			t.Fatalf("Origin(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAllows(t *testing.T) {
	ref := Bind("chkey:c1:chk-1", "https://relay.example.com/v1")
	if ref != "chkey:c1:chk-1#https://relay.example.com:443" {
		t.Fatalf("Bind = %q", ref)
	}
	if !Allows(ref, "c1", "https://relay.example.com") {
		t.Fatal("same origin must be allowed")
	}
	for _, target := range []string{"https://attacker.example", "http://relay.example.com", "https://relay.example.com:8443"} {
		if Allows(ref, "c1", target) {
			t.Fatalf("bound key released to %s", target)
		}
	}
	if Allows(ref, "c2", "https://relay.example.com") {
		t.Fatal("another channel must not use c1's key ref")
	}
	if Allows("chkey:c1:chk-legacy", "c2", "https://x") {
		t.Fatal("legacy channel-key refs stay scoped to their channel")
	}
	if !Allows("chkey:c1:chk-legacy", "c1", "https://anything") || !Unbound("chkey:c1:chk-legacy") {
		t.Fatal("legacy unbound ref semantics changed")
	}
	if !Allows("acct:c1", "c1", "https://x") {
		t.Fatal("non channel-key refs without binding are unaffected")
	}
}
