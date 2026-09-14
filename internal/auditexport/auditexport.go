package auditexport

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const (
	SchemaVersion = 1
	MaxFieldBytes = 256
)

// Metadata is the bounded transport-neutral subset of audit metadata that may
// leave Skillet. Arbitrary details/claims are intentionally not part of the
// export contract.
type Metadata struct {
	ActorType    string
	ActorID      string
	RepositoryID string
	SkillID      string
	RevisionID   string
	RequestID    string
}

// Event is the stable audit export envelope. It deliberately contains no
// bearer credential, raw claim map, query text, transcript, or arbitrary
// details payload.
type Event struct {
	SchemaVersion  int       `json:"schema_version"`
	OrganizationID string    `json:"organization_id"`
	EventType      string    `json:"event_type"`
	OccurredAt     time.Time `json:"occurred_at"`
	ActorType      string    `json:"actor_type,omitempty"`
	ActorID        string    `json:"actor_id,omitempty"`
	RepositoryID   string    `json:"repository_id,omitempty"`
	SkillID        string    `json:"skill_id,omitempty"`
	RevisionID     string    `json:"revision_id,omitempty"`
	RequestID      string    `json:"request_id,omitempty"`
}

func NewEvent(organizationID, eventType string, occurredAt time.Time, metadata Metadata) Event {
	return normalize(Event{
		OrganizationID: organizationID,
		EventType:      eventType,
		OccurredAt:     occurredAt,
		ActorType:      metadata.ActorType,
		ActorID:        metadata.ActorID,
		RepositoryID:   metadata.RepositoryID,
		SkillID:        metadata.SkillID,
		RevisionID:     metadata.RevisionID,
		RequestID:      metadata.RequestID,
	})
}

// Sink is the only transport-specific boundary. Implementations must export
// exactly the normalized envelope they receive; Skillet never passes arbitrary
// audit details through this interface.
type Sink interface {
	Export(context.Context, Event) error
}

type FailureObserver func(Event, error)

// Exporter makes a Sink best-effort. Sink errors and panics are counted and
// observed but never propagate into authoritative catalogue operations.
type Exporter struct {
	sink      Sink
	onFailure FailureObserver
	attempts  atomic.Uint64
	failures  atomic.Uint64
}

func New(sink Sink, onFailure FailureObserver) *Exporter {
	return &Exporter{sink: sink, onFailure: onFailure}
}

func (e *Exporter) Export(ctx context.Context, event Event) {
	if e == nil || e.sink == nil {
		return
	}
	event = normalize(event)
	e.attempts.Add(1)
	defer func() {
		if recovered := recover(); recovered != nil {
			e.reportFailure(event, fmt.Errorf("audit sink panic: %v", recovered))
		}
	}()
	if err := e.sink.Export(ctx, event); err != nil {
		e.reportFailure(event, err)
	}
}

func (e *Exporter) Attempts() uint64 {
	if e == nil {
		return 0
	}
	return e.attempts.Load()
}

func (e *Exporter) Failures() uint64 {
	if e == nil {
		return 0
	}
	return e.failures.Load()
}

func (e *Exporter) reportFailure(event Event, err error) {
	e.failures.Add(1)
	if e.onFailure == nil {
		return
	}
	func() {
		defer func() { _ = recover() }()
		e.onFailure(event, err)
	}()
}

func normalize(event Event) Event {
	event.SchemaVersion = SchemaVersion
	event.OrganizationID = sanitize(event.OrganizationID)
	event.EventType = sanitize(event.EventType)
	event.ActorType = sanitize(event.ActorType)
	event.ActorID = sanitize(event.ActorID)
	event.RepositoryID = sanitize(event.RepositoryID)
	event.SkillID = sanitize(event.SkillID)
	event.RevisionID = sanitize(event.RevisionID)
	event.RequestID = sanitize(event.RequestID)
	if !event.OccurredAt.IsZero() {
		event.OccurredAt = event.OccurredAt.UTC()
	}
	return event
}

func sanitize(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= len("bearer ") && strings.EqualFold(value[:len("bearer ")], "bearer ") {
		return "[redacted]"
	}
	if len(value) <= MaxFieldBytes && utf8.ValidString(value) {
		return value
	}
	for len(value) > MaxFieldBytes || !utf8.ValidString(value) {
		_, size := utf8.DecodeLastRuneInString(value)
		if size <= 0 {
			return ""
		}
		value = value[:len(value)-size]
	}
	return value
}
