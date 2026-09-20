package logging

import (
	"reflect"
	"regexp"
	"strings"
)

var (
	keyValuePattern = regexp.MustCompile(`(?i)(\b(?:authorization|cookie|set-cookie|proxy-authorization|x-api-key|api-key|api_key|token|password|secret|access_token|refresh_token|client_secret)\s*[:=]\s*)([^\s,;&]+(?:\s+[^\s,;&]+)?)`)
	bearerPattern   = regexp.MustCompile(`(?i)(\b(?:authorization\s*[:=]\s*)?bearer\s+)[^\s,;&]+`)
	basicPattern    = regexp.MustCompile(`(?i)(\b(?:authorization\s*[:=]\s*)?basic\s+)[^\s,;&]+`)
	apiPattern      = regexp.MustCompile(`(?i)\b(?:sk|ak|ah|ghp|gho|xoxb|xoxp)[_-][A-Za-z0-9._-]+\b`)
	oauthPattern    = regexp.MustCompile(`(?i)([?&](?:code|state|access_token|refresh_token|client_secret|token|key|password)=)[^&#\s]+`)
)

type Redactor struct{}

func NewRedactor() *Redactor { return &Redactor{} }

func (r *Redactor) Redact(value string) string {
	value = bearerPattern.ReplaceAllString(value, `${1}[REDACTED]`)
	value = basicPattern.ReplaceAllString(value, `${1}[REDACTED]`)
	value = keyValuePattern.ReplaceAllString(value, `${1}[REDACTED]`)
	value = oauthPattern.ReplaceAllString(value, `${1}[REDACTED]`)
	return apiPattern.ReplaceAllString(value, `[REDACTED]`)
}

func (r *Redactor) Header(name, value string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "cookie", "set-cookie", "proxy-authorization", "x-api-key", "api-key", "api_key":
		return "[REDACTED]"
	default:
		return r.Redact(value)
	}
}

// RedactValue recursively sanitizes structured fields before they reach a log
// or audit sink. Unknown values are stringified only as a last resort.
func (r *Redactor) RedactValue(value any) any {
	return r.redactReflect(reflect.ValueOf(value))
}

func (r *Redactor) redactReflect(value reflect.Value) any {
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil
		}
		return r.redactReflect(value.Elem())
	}
	if value.Kind() == reflect.String {
		return r.Redact(value.String())
	}
	switch value.Kind() {
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return value.Interface()
		}
		out := make(map[string]any, value.Len())
		iter := value.MapRange()
		for iter.Next() {
			key := iter.Key().String()
			if r.isSensitiveKey(key) {
				out[key] = "[REDACTED]"
			} else {
				out[key] = r.redactReflect(iter.Value())
			}
		}
		return out
	case reflect.Slice, reflect.Array:
		out := make([]any, value.Len())
		for i := 0; i < value.Len(); i++ {
			out[i] = r.redactReflect(value.Index(i))
		}
		return out
	default:
		return value.Interface()
	}
}

func (r *Redactor) IsSensitiveKey(key string) bool {
	return r.isSensitiveKey(key)
}

func (r *Redactor) isSensitiveKey(key string) bool {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "-", "_")) {
	case "authorization", "cookie", "set_cookie", "proxy_authorization", "x_api_key", "api_key", "token", "access_token", "refresh_token", "password", "secret", "client_secret", "code", "state":
		return true
	default:
		return false
	}
}
