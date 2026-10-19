package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
)

type DecisionQuestionGenerationSettings struct {
	Enabled bool   `json:"enabled"`
	Model   string `json:"model,omitempty"`
}

func (s DecisionQuestionGenerationSettings) Value() (driver.Value, error) {
	body, err := json.Marshal(s)
	return string(body), err
}
func (s *DecisionQuestionGenerationSettings) Scan(value interface{}) error {
	*s = DecisionQuestionGenerationSettings{}
	if value == nil {
		return nil
	}
	var body []byte
	switch v := value.(type) {
	case []byte:
		body = v
	case string:
		body = []byte(v)
	default:
		return fmt.Errorf("invalid decision generation settings storage")
	}
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, s)
}
func (s DecisionQuestionGenerationSettings) Validate() error {
	if len(s.Model) > 200 || strings.ContainsAny(s.Model, "\r\n\x00") {
		return fmt.Errorf("invalid question generation model")
	}
	return nil
}
