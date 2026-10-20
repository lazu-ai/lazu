package channel

// StreamLog is a stream's upstream events kept for the exchange log, bounded
// by bytes: past the limit it keeps counting and says it truncated.
type StreamLog struct {
	limit     int
	size      int
	received  int
	chunks    []string
	truncated bool
}

func NewStreamLog(limit int) *StreamLog {
	return &StreamLog{limit: limit}
}

func (b *StreamLog) Add(chunk string) {
	if b == nil || chunk == "" {
		return
	}
	b.received += len(chunk)
	if b.size+len(chunk) > b.limit {
		b.truncated = true
		return
	}
	b.chunks = append(b.chunks, chunk)
	b.size += len(chunk)
}

func (b *StreamLog) Values() []string {
	if b == nil {
		return nil
	}
	return append([]string(nil), b.chunks...)
}

func (b *StreamLog) Metadata() map[string]any {
	if b == nil || !b.truncated {
		return nil
	}
	return map[string]any{"response_chunks_truncated": true, "response_chunks_retained_bytes": b.size, "response_bytes_received": b.received}
}
