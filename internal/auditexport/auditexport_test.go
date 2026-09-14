package auditexport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type sinkFunc func(context.Context, Event) error

func (f sinkFunc) Export(ctx context.Context, event Event) error { return f(ctx, event) }

func TestEventEnvelopeIsBoundedAndDoesNotExposeArbitraryClaims(t *testing.T) {
	occurred := time.Date(2026, 9, 14, 7, 30, 0, 123, time.FixedZone("test", 3600))
	event := NewEvent(strings.Repeat("o", 400), "authorization_denied", occurred, Metadata{
		ActorType:    "agent",
		ActorID:      "Bearer super-secret-token",
		RepositoryID: strings.Repeat("r", 400),
		RequestID:    "req-123",
	})
	if event.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %d", event.SchemaVersion)
	}
	if event.ActorID != "[redacted]" {
		t.Fatalf("actor id = %q", event.ActorID)
	}
	if len(event.OrganizationID) > MaxFieldBytes || len(event.RepositoryID) > MaxFieldBytes {
		t.Fatalf("envelope fields were not bounded: org=%d repository=%d", len(event.OrganizationID), len(event.RepositoryID))
	}
	if event.OccurredAt.Location() != time.UTC {
		t.Fatalf("occurred_at location = %v", event.OccurredAt.Location())
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"super-secret-token", "claims", "details"} {
		if bytes.Contains(payload, []byte(forbidden)) {
			t.Fatalf("export payload contains forbidden value %q: %s", forbidden, payload)
		}
	}
}

func TestExporterFailureIsBestEffortAndCounted(t *testing.T) {
	want := errors.New("sink unavailable")
	var observed error
	exporter := New(sinkFunc(func(context.Context, Event) error { return want }), func(_ Event, err error) {
		observed = err
	})
	exporter.Export(context.Background(), NewEvent("demo", "search_executed", time.Now(), Metadata{RequestID: "req-1"}))
	if exporter.Attempts() != 1 || exporter.Failures() != 1 {
		t.Fatalf("attempts=%d failures=%d", exporter.Attempts(), exporter.Failures())
	}
	if !errors.Is(observed, want) {
		t.Fatalf("observed error = %v", observed)
	}
}

func TestExporterRecoversSinkAndObserverPanics(t *testing.T) {
	exporter := New(sinkFunc(func(context.Context, Event) error { panic("sink panic") }), func(Event, error) {
		panic("observer panic")
	})
	exporter.Export(context.Background(), NewEvent("demo", "skill_admitted", time.Now(), Metadata{}))
	if exporter.Failures() != 1 {
		t.Fatalf("failures = %d", exporter.Failures())
	}
}

func TestJSONLinesSinkWritesOneStableEnvelopePerLine(t *testing.T) {
	var out bytes.Buffer
	sink, err := NewJSONLinesSink(&out)
	if err != nil {
		t.Fatal(err)
	}
	event := NewEvent("demo", "repository_sync_started", time.Date(2026, 9, 14, 7, 0, 0, 0, time.UTC), Metadata{RepositoryID: "demo/skills", RequestID: "req-7"})
	if err := sink.Export(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(out.String(), "\n"); got != 1 {
		t.Fatalf("newline count = %d output=%q", got, out.String())
	}
	var decoded Event
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != SchemaVersion || decoded.OrganizationID != "demo" || decoded.RepositoryID != "demo/skills" || decoded.RequestID != "req-7" {
		t.Fatalf("decoded envelope = %+v", decoded)
	}
}
