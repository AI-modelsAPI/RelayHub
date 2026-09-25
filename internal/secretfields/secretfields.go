// Package secretfields identifies and masks credential material that lives
// inside otherwise non-secret channel configuration: passwords embedded in
// proxy URLs and credential-bearing custom headers. The management API, MCP
// output and configuration exports share these rules so a value hidden in
// one place is not disclosed by another (AUDIT 2026-09-24 F10).
package secretfields

import (
	"net/url"
	"strings"

	"relayhub/internal/domain"
)

// Redacted replaces sensitive custom header values in any echo or export.
const Redacted = "[redacted]"

// SensitiveHeader reports whether a custom header name carries a credential.
func SensitiveHeader(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	switch n {
	case "authorization", "proxy-authorization", "cookie", "x-api-key", "api-key", "apikey":
		return true
	}
	for _, part := range []string{"token", "secret", "password", "passwd", "session", "auth", "key"} {
		if strings.Contains(n, part) {
			return true
		}
	}
	return false
}

// MaskHeaders returns a copy of h with sensitive values replaced by Redacted.
func MaskHeaders(h map[string]string) map[string]string {
	if len(h) == 0 {
		return h
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		if SensitiveHeader(k) && v != "" {
			v = Redacted
		}
		out[k] = v
	}
	return out
}

// MaskProxyURL removes the password from a proxy URL, keeping the username
// so operators can still tell which account is configured.
func MaskProxyURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil {
		return raw
	}
	if u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.User(u.User.Username())
		}
	}
	return u.String()
}

// MaskChannel returns ch with its embedded credentials masked.
func MaskChannel(ch domain.Channel) domain.Channel {
	ch.ProxyURL = MaskProxyURL(ch.ProxyURL)
	ch.CustomHeaders = MaskHeaders(ch.CustomHeaders)
	return ch
}

// RestoreMasked puts stored credentials back where an incoming channel only
// carries their masked forms (a GET-modify-PUT round trip, or a password-less
// export being re-imported). Redacted headers without a stored value are
// dropped rather than sent upstream literally.
func RestoreMasked(c *domain.Channel, existing domain.Channel) {
	if c.ProxyURL != "" && c.ProxyURL != existing.ProxyURL && c.ProxyURL == MaskProxyURL(existing.ProxyURL) {
		c.ProxyURL = existing.ProxyURL
	}
	for k, v := range c.CustomHeaders {
		if v != Redacted {
			continue
		}
		if old, ok := existing.CustomHeaders[k]; ok {
			c.CustomHeaders[k] = old
		} else {
			delete(c.CustomHeaders, k)
		}
	}
}
