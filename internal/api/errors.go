package api

import (
	"encoding/json"
	"net/http"
)

// Error is the stable management API error envelope.
type Error struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"request_id"`
	Details   map[string]any `json:"details,omitempty"`
}

type fault struct {
	status  int
	code    string
	message string
	details map[string]any
	cause   error
}

func (f fault) Error() string { return f.message }

func badRequest(code, message string) error {
	return fault{status: http.StatusBadRequest, code: code, message: message}
}
func unauthorized() error {
	return fault{status: http.StatusUnauthorized, code: "authentication_error", message: "management authentication required"}
}
func forbidden(message string) error {
	return fault{status: http.StatusForbidden, code: "authorization_error", message: message}
}
func notFound(message string) error {
	return fault{status: http.StatusNotFound, code: "not_found", message: message}
}
func conflict(message string) error {
	return fault{status: http.StatusConflict, code: "conflict", message: message}
}
func unsupported(message string) error {
	return fault{status: http.StatusNotImplemented, code: "unsupported", message: message}
}
func unavailable(message string) error {
	return fault{status: http.StatusServiceUnavailable, code: "service_unavailable", message: message}
}
func methodNotAllowed() error {
	return fault{status: http.StatusMethodNotAllowed, code: "method_not_allowed", message: "method not allowed for this endpoint"}
}
func internal(err error) error {
	return fault{status: http.StatusInternalServerError, code: "internal_error", message: "internal management error", cause: err}
}

func encodeError(w http.ResponseWriter, requestID string, err error) {
	status := http.StatusInternalServerError
	code := "internal_error"
	message := "internal management error"
	var details map[string]any
	if f, ok := err.(fault); ok {
		status, code, message, details = f.status, f.code, f.message, f.details
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": Error{Code: code, Message: message, RequestID: requestID, Details: details}})
}
