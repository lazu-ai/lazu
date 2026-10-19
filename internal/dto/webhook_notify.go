package dto

import "errors"

// WebhookNotifyPayload is the JSON payload sent to configured webhook receivers.
type WebhookNotifyPayload struct {
	Type      string        `json:"type"`
	Title     string        `json:"title"`
	Content   string        `json:"content"`
	Values    []interface{} `json:"values,omitempty"`
	Timestamp int64         `json:"timestamp"`
}

func (p *WebhookNotifyPayload) Validate() error {
	if p == nil {
		return errors.New("nil payload")
	}
	if p.Type == "" {
		return errors.New("type is required")
	}
	if p.Title == "" {
		return errors.New("title is required")
	}
	if p.Content == "" {
		return errors.New("content is required")
	}
	if p.Timestamp <= 0 {
		return errors.New("timestamp must be positive")
	}
	return nil
}
