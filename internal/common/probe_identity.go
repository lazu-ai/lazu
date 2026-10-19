package common

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ProbeClientClaudeCode keys the probe identity for sources restricted to
// Claude Code clients (ChannelOtherSettings.ClaudeCodeClientOnly).
const ProbeClientClaudeCode = "claude_code"

// ProbeIdentityFor returns the configured probe identity for a client
// restriction, and whether one is configured.
func ProbeIdentityFor(client string) (ProbeIdentityConfig, bool) {
	identity, ok := AppConfig.ProbeIdentities[client]
	if !ok {
		return ProbeIdentityConfig{}, false
	}
	return identity, identity.UserAgent != "" || len(identity.Headers) > 0 || identity.Metadata != ""
}

// RenderProbeMetadata fills a probe identity's metadata template for one
// source: {{device_id}} is a stable id derived from the source id, and
// {{session_id}} is a fresh UUID. The result must be a JSON object.
func RenderProbeMetadata(template string, channelID int) (json.RawMessage, error) {
	if strings.TrimSpace(template) == "" {
		return nil, nil
	}
	device := sha256.Sum256([]byte(fmt.Sprintf("lazu-probe-device/%d", channelID)))
	rendered := strings.NewReplacer(
		"{{device_id}}", hex.EncodeToString(device[:]),
		"{{session_id}}", uuid.NewString(),
	).Replace(template)
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rendered), &object); err != nil {
		return nil, fmt.Errorf("probe metadata is not a JSON object: %w", err)
	}
	if object == nil {
		return nil, errors.New("probe metadata is not a JSON object")
	}
	return json.RawMessage(rendered), nil
}
