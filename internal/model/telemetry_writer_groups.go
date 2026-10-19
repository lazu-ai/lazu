package model

import (
	"encoding/json"

	"github.com/go-redis/redis/v8"
)

const (
	relayWriterMaxRequests = 500
	relayWriterMaxMessages = 4000
	relayWriterMaxBytes    = 16 << 20
)

// The limit counts requests, rather than the several stream entries emitted
// by each request. Independent message/byte limits also bound retry and drain
// batches. Oversized single messages are isolated and rejected by the writer.
type relayWriterBounds struct {
	requests map[string]bool
	messages int
	bytes    int
}

func relayMessageBytes(message redis.XMessage) int {
	kind, _ := redisReplyString(message.Values["type"])
	payload, _ := redisReplyString(message.Values["payload"])
	return len(message.ID) + len(kind) + len(payload)
}

func relayMessageRequestKey(message redis.XMessage) string {
	payload, _ := redisReplyString(message.Values["payload"])
	var identity struct {
		RequestID  string `json:"request_id"`
		ConsumeLog *struct {
			RequestID string `json:"request_id"`
		} `json:"consume_log"`
		Outcome *struct {
			RequestID string `json:"request_id"`
		} `json:"routing_outcome"`
	}
	if json.Unmarshal([]byte(payload), &identity) == nil {
		if identity.RequestID != "" {
			return "request:" + identity.RequestID
		}
		if identity.ConsumeLog != nil && identity.ConsumeLog.RequestID != "" {
			return "request:" + identity.ConsumeLog.RequestID
		}
		if identity.Outcome != nil && identity.Outcome.RequestID != "" {
			return "request:" + identity.Outcome.RequestID
		}
	}
	// Legacy facts without a request identity must not collapse together.
	return "message:" + message.ID
}

func (bounds *relayWriterBounds) add(message redis.XMessage) {
	if bounds.requests == nil {
		bounds.requests = make(map[string]bool)
	}
	bounds.requests[relayMessageRequestKey(message)] = true
	bounds.messages++
	bounds.bytes += relayMessageBytes(message)
}

func (bounds relayWriterBounds) readCount() int64 {
	remaining := relayWriterMaxRequests - len(bounds.requests)
	if messages := relayWriterMaxMessages - bounds.messages; messages < remaining {
		remaining = messages
	}
	if bounds.bytes >= relayWriterMaxBytes || remaining <= 0 {
		return 0
	}
	return int64(remaining)
}

func splitRelayWriterMessages(messages []redis.XMessage) [][]redis.XMessage {
	var batches [][]redis.XMessage
	start := 0
	bounds := relayWriterBounds{}
	for i, message := range messages {
		key := relayMessageRequestKey(message)
		if i > start && (bounds.messages >= relayWriterMaxMessages || bounds.bytes+relayMessageBytes(message) > relayWriterMaxBytes || (!bounds.requests[key] && len(bounds.requests) >= relayWriterMaxRequests)) {
			batches = append(batches, messages[start:i])
			start = i
			bounds = relayWriterBounds{}
		}
		bounds.add(message)
	}
	if start < len(messages) {
		batches = append(batches, messages[start:])
	}
	return batches
}

func groupRelayWriterMessages(messages []redis.XMessage) [][]redis.XMessage {
	groups := make([][]redis.XMessage, 0)
	indices := make(map[string]int)
	for _, message := range messages {
		key := relayMessageRequestKey(message)
		index, exists := indices[key]
		if !exists {
			index = len(groups)
			indices[key] = index
			groups = append(groups, nil)
		}
		groups[index] = append(groups[index], message)
	}
	return groups
}
