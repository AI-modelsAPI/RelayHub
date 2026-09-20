package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"relayhub/internal/logging"
)

type Event struct {
	ID        string
	Action    string
	RequestID string
	TaskID    string
	Actor     string
	At        time.Time
	Metadata  map[string]string
}

type Sink interface {
	Write(context.Context, Event) error
}

type Logger struct {
	Sink     Sink
	Redactor *logging.Redactor
}

type SQLSink struct{ DB *sql.DB }

func NewSQLSink(db *sql.DB) (*SQLSink, error) {
	if db == nil {
		return nil, errors.New("audit database is nil")
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS audit_events (id TEXT PRIMARY KEY NOT NULL, action TEXT NOT NULL, request_id TEXT, task_id TEXT, actor TEXT, metadata_json TEXT NOT NULL, created_at TEXT NOT NULL)`); err != nil {
		return nil, err
	}
	return &SQLSink{DB: db}, nil
}

func (s *SQLSink) Write(ctx context.Context, e Event) error {
	if s == nil || s.DB == nil {
		return errors.New("audit database is nil")
	}
	metadata, err := json.Marshal(e.Metadata)
	if err != nil {
		return err
	}
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO audit_events(id,action,request_id,task_id,actor,metadata_json,created_at) VALUES(?,?,?,?,?,?,?)`, e.ID, e.Action, e.RequestID, e.TaskID, e.Actor, metadata, e.At.Format(time.RFC3339Nano))
	return err
}

var ErrInvalidEvent = errors.New("invalid audit event")

func (l Logger) Record(ctx context.Context, e Event) error {
	if strings.TrimSpace(e.Action) == "" {
		return ErrInvalidEvent
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	} else {
		e.At = e.At.UTC()
	}
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if l.Sink == nil {
		return nil
	}
	redactor := l.Redactor
	if redactor == nil {
		redactor = logging.NewRedactor()
	}
	metadata := make(map[string]string, len(e.Metadata))
	for key, value := range e.Metadata {
		if redactor.IsSensitiveKey(key) {
			metadata[key] = "[REDACTED]"
		} else {
			metadata[key] = redactor.Redact(value)
		}
	}
	e.Metadata = metadata
	e.Action = redactor.Redact(e.Action)
	e.RequestID = redactor.Redact(e.RequestID)
	e.TaskID = redactor.Redact(e.TaskID)
	e.Actor = redactor.Redact(e.Actor)
	return l.Sink.Write(ctx, e)
}
