// Package keybind ties stored upstream credentials to the origin they were
// entered for.
//
// Upstream API keys are write-only in the management API, but they used to be
// *forwardable*: pointing a channel's base_url at another host and running a
// channel test sent the stored key there as a Bearer token (AUDIT 2026-09-24
// F5). A key's secret ref now carries the origin ("scheme://host:port") it was
// created for. The secret store seals values with the ref as AEAD additional
// data, so the binding cannot be moved to another ref, and a key is only ever
// released to requests whose target origin matches. Re-binding requires
// re-entering the key, which proves possession.
package keybind

import (
	"net/url"
	"strings"
)

// ChannelKeyPrefix is the secret-ref namespace for per-channel API keys:
// "chkey:<channel id>:<key id>[#<origin>]".
const ChannelKeyPrefix = "chkey:"

// Origin normalises a base URL to "scheme://host:port" (lower-case, default
// port explicit). It returns "" for URLs without an http(s) scheme or host.
func Origin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	switch scheme {
	case "http":
		if port == "" {
			port = "80"
		}
	case "https":
		if port == "" {
			port = "443"
		}
	default:
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return scheme + "://" + host + ":" + port
}

// Bind appends the origin of baseURL to ref. Unparseable base URLs leave the
// ref unbound (such channels cannot send requests anyway).
func Bind(ref, baseURL string) string {
	if o := Origin(baseURL); o != "" {
		return ref + "#" + o
	}
	return ref
}

// RefOrigin returns the origin a ref is bound to, if any.
func RefOrigin(ref string) (string, bool) {
	i := strings.LastIndex(ref, "#")
	if i < 0 {
		return "", false
	}
	return ref[i+1:], true
}

// Unbound reports whether ref is a legacy channel key without an origin.
func Unbound(ref string) bool {
	_, bound := RefOrigin(ref)
	return strings.HasPrefix(ref, ChannelKeyPrefix) && !bound
}

// Allows reports whether the secret stored under ref may be sent on behalf of
// channelID to baseURL: channel-key refs must belong to that channel (a
// channel's credential_ref could otherwise name another channel's key), and
// bound refs must match the target origin.
func Allows(ref, channelID, baseURL string) bool {
	if strings.HasPrefix(ref, ChannelKeyPrefix) {
		rest := strings.TrimPrefix(ref, ChannelKeyPrefix)
		owner, _, ok := strings.Cut(rest, ":")
		if !ok || owner != channelID {
			return false
		}
	}
	if o, bound := RefOrigin(ref); bound {
		return o != "" && o == Origin(baseURL)
	}
	return true
}
