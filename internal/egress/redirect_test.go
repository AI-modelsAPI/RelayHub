package egress

import (
	"net/http"
	"net/url"
	"testing"
)

func TestSameOriginRedirectPolicy(t *testing.T) {
	mk := func(raw string) *http.Request {
		u, _ := url.Parse(raw)
		return &http.Request{URL: u}
	}
	cases := []struct {
		from, to string
		follow   bool
	}{
		{"https://relay.example.com/v1/models", "https://relay.example.com/api/v1/models", true},
		{"http://relay.example.com/v1", "https://relay.example.com/v1", true},
		{"https://relay.example.com/v1", "http://relay.example.com/v1", false},
		{"https://relay.example.com/v1", "https://evil.example.com/v1", false},
		{"http://127.0.0.1:3000/v1", "http://127.0.0.1:8790/api/v1/keys", false},
		{"https://relay.example.com/v1", "http://169.254.169.254/latest/meta-data", false},
	}
	for _, c := range cases {
		err := SameOriginRedirect(mk(c.to), []*http.Request{mk(c.from)})
		if (err == nil) != c.follow {
			t.Fatalf("%s -> %s: follow=%v, want %v", c.from, c.to, err == nil, c.follow)
		}
	}
	via := []*http.Request{mk("https://a.example/1"), mk("https://a.example/2"), mk("https://a.example/3")}
	if SameOriginRedirect(mk("https://a.example/4"), via) == nil {
		t.Fatal("redirect chains must be capped")
	}
	if c := newClient("", 0); c.CheckRedirect == nil {
		t.Fatal("egress clients must install the redirect policy")
	}
}
