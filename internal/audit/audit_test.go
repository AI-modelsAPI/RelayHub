package audit

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

type captureSink struct{ event Event }

func (s *captureSink) Write(_ context.Context, event Event) error { s.event = event; return nil }

func TestRecordRedactsMetadataAndAddsTimestamp(t *testing.T) {
	sink := &captureSink{}
	logger := Logger{Sink: sink}
	err := logger.Record(context.Background(), Event{Action: "credential.updated", RequestID: "req-1", TaskID: "task-1", Actor: "local", Metadata: map[string]string{"authorization": "Bearer secret", "model": "gpt-5.6-sol"}})
	if err != nil {
		t.Fatal(err)
	}
	if sink.event.At.IsZero() || sink.event.At.Location() != time.UTC {
		t.Fatalf("missing UTC timestamp: %#v", sink.event)
	}
	if sink.event.ID == "" {
		t.Fatal("missing collision-safe event ID")
	}
	if strings.Contains(sink.event.Metadata["authorization"], "secret") || sink.event.Metadata["authorization"] != "[REDACTED]" {
		t.Fatalf("secret leaked: %#v", sink.event.Metadata)
	}
	if sink.event.Metadata["model"] != "gpt-5.6-sol" {
		t.Fatalf("ordinary metadata changed: %#v", sink.event.Metadata)
	}
}

func TestRecordRejectsMissingAction(t *testing.T) {
	if err := (Logger{}).Record(context.Background(), Event{}); err != ErrInvalidEvent {
		t.Fatalf("got %v, want %v", err, ErrInvalidEvent)
	}
}

func TestSQLSinkPersistsOnlyRedactedMetadata(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sink, err := NewSQLSink(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := (Logger{Sink: sink}).Record(context.Background(), Event{Action: "request", RequestID: "r", At: time.Now(), Metadata: map[string]string{"token": "secret-token", "model": "gpt-5.6-sol"}}); err != nil {
		t.Fatal(err)
	}
	var metadata string
	if err := db.QueryRow(`SELECT metadata_json FROM audit_events`).Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(metadata, "secret-token") || !strings.Contains(metadata, "gpt-5.6-sol") {
		t.Fatalf("unexpected persisted metadata: %s", metadata)
	}
}

func TestSQLSinkAllowsIdenticalEvents(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sink, err := NewSQLSink(db)
	if err != nil {
		t.Fatal(err)
	}
	event := Event{Action: "request", RequestID: "same", TaskID: "same", At: time.Unix(1, 2), Metadata: map[string]string{}}
	logger := Logger{Sink: sink}
	if err := logger.Record(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if err := logger.Record(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM audit_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("audit rows = %d, want 2", count)
	}
}

func TestSQLSinkUsesCallerContext(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sink, err := NewSQLSink(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sink.Write(ctx, Event{Action: "request", Metadata: map[string]string{}}); err == nil {
		t.Fatal("expected canceled context error")
	}
}
