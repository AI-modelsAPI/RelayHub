package logging

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

type Logger struct {
	out      io.Writer
	redactor *Redactor
	mu       sync.Mutex
}

func New(out io.Writer) *Logger {
	if out == nil {
		out = io.Discard
	}
	return &Logger{out: out, redactor: NewRedactor()}
}

func (l *Logger) Event(level, kind, requestID string, fields map[string]any) error {
	event := map[string]any{
		"time":       time.Now().UTC().Format(time.RFC3339Nano),
		"level":      l.redactor.Redact(level),
		"event":      l.redactor.Redact(kind),
		"request_id": l.redactor.Redact(requestID),
	}
	for key, value := range fields {
		event[key] = l.redactField(key, value)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return json.NewEncoder(l.out).Encode(event)
}

func (l *Logger) redactField(key string, value any) any {
	if l.redactor.isSensitiveKey(key) {
		return "[REDACTED]"
	}
	return l.redactor.RedactValue(value)
}
