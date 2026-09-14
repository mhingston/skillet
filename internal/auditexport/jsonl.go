package auditexport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// JSONLinesSink is the deterministic reference sink. Each event is encoded as
// one JSON object followed by a newline. The mutex keeps concurrent writes from
// interleaving when stdout or one file is shared by multiple callers.
type JSONLinesSink struct {
	writer io.Writer
	mu     sync.Mutex
}

func NewJSONLinesSink(writer io.Writer) (*JSONLinesSink, error) {
	if writer == nil {
		return nil, fmt.Errorf("audit JSONL writer is required")
	}
	return &JSONLinesSink{writer: writer}, nil
}

func (s *JSONLinesSink) Export(ctx context.Context, event Event) error {
	if s == nil || s.writer == nil {
		return fmt.Errorf("audit JSONL writer is required")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	payload, err := json.Marshal(normalize(event))
	if err != nil {
		return fmt.Errorf("encode audit event: %w", err)
	}
	payload = append(payload, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.writer.Write(payload)
	if err != nil {
		return fmt.Errorf("write audit event: %w", err)
	}
	if n != len(payload) {
		return io.ErrShortWrite
	}
	return nil
}
