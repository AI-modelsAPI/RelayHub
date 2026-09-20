package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagementBrowserBoundary(t *testing.T) {
	srv := httptest.NewServer(testServer(t, ""))
	defer srv.Close()
	for _, tc := range []struct {
		name, origin, host, fetch string
		want                      int
	}{
		{"cross-origin", "https://evil.invalid", "", "", 403},
		{"null-origin", "null", "", "", 403},
		{"cross-site-fetch", "", "", "cross-site", 403},
		{"rebound-host", "", "evil.invalid", "", 403},
		{"same-origin", srv.URL, "", "same-origin", 200},
		{"native-client", "", "", "", 200},
		{"ipv6-default-port", "http://[::1]", "[::1]", "same-origin", 200},
		{"ipv6-explicit-port", "http://[::1]:8790", "[::1]:8790", "same-origin", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest("POST", srv.URL+"/api/v1/providers", strings.NewReader(`{"id":"`+tc.name+`","name":"probe","protocol":"openai-chat"}`))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "text/plain")
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.host != "" {
				req.Host = tc.host
			}
			if tc.fetch != "" {
				req.Header.Set("Sec-Fetch-Site", tc.fetch)
			}
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tc.want {
				t.Fatalf("got %d want %d: %s", resp.StatusCode, tc.want, body)
			}
		})
	}
}
