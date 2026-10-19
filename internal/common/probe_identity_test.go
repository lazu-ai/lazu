package common

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The metadata shape a client-restricted pool expects: user_id is itself a
// JSON string naming a device and a session.
const testProbeMetadata = `{"user_id":"{\"account_uuid\":\"\",\"device_id\":\"{{device_id}}\",\"session_id\":\"{{session_id}}\"}"}`

func readProbeUserID(t *testing.T, raw json.RawMessage) map[string]string {
	t.Helper()
	var outer map[string]string
	if err := json.Unmarshal(raw, &outer); err != nil {
		t.Fatal(err)
	}
	var inner map[string]string
	if err := json.Unmarshal([]byte(outer["user_id"]), &inner); err != nil {
		t.Fatalf("user_id is not a JSON string: %v", err)
	}
	return inner
}

// A device is stable per source and differs between sources; a session is
// fresh on every probe.
func TestRenderProbeMetadataDeviceAndSession(t *testing.T) {
	render := func(channelID int) map[string]string {
		raw, err := RenderProbeMetadata(testProbeMetadata, channelID)
		if err != nil {
			t.Fatal(err)
		}
		return readProbeUserID(t, raw)
	}
	a, b, other := render(87), render(87), render(86)
	if len(a["device_id"]) != 64 || a["device_id"] != b["device_id"] || a["device_id"] == other["device_id"] {
		t.Fatalf("device ids: %v %v %v", a, b, other)
	}
	if a["session_id"] == "" || a["session_id"] == b["session_id"] {
		t.Fatal("session must be fresh per probe")
	}
	if _, ok := a["account_uuid"]; !ok {
		t.Fatal("account_uuid key missing")
	}
}

// Moving the identity from code into config.prod.yaml must not change a
// single byte the gateway sends: this rebuilds the metadata the way the code
// built it before and compares, with the random session pinned.
func TestRenderProbeMetadataMatchesPreviousEncoding(t *testing.T) {
	raw, err := RenderProbeMetadata(testProbeMetadata, 87)
	if err != nil {
		t.Fatal(err)
	}
	session := readProbeUserID(t, raw)["session_id"]
	device := sha256.Sum256([]byte(fmt.Sprintf("lazu-probe-device/%d", 87)))
	userID, _ := json.Marshal(map[string]string{
		"device_id":    hex.EncodeToString(device[:]),
		"account_uuid": "",
		"session_id":   session,
	})
	previous, _ := json.Marshal(map[string]string{"user_id": string(userID)})
	if string(raw) != string(previous) {
		t.Fatalf("rendered %s\nprevious %s", raw, previous)
	}
}

func TestRenderProbeMetadataRejectsNonObjects(t *testing.T) {
	for _, template := range []string{`not json`, `[1,2]`, `"text"`, `null`} {
		if _, err := RenderProbeMetadata(template, 1); err == nil {
			t.Fatalf("template %q accepted", template)
		}
	}
	if raw, err := RenderProbeMetadata("  ", 1); err != nil || raw != nil {
		t.Fatalf("empty template = %s, %v; want nothing", raw, err)
	}
}

// The production config file carries the identity; the public template
// carries none, so a self-hosted gateway presents no client identity until
// its operator configures one.
func TestProbeIdentitiesLoadFromConfig(t *testing.T) {
	originalConfig := AppConfig
	t.Cleanup(func() { AppConfig = originalConfig })

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
probe_identities:
  claude_code:
    user_agent: "client/1.0"
    headers:
      x-app: cli
    metadata: '`+testProbeMetadata+`'
`), 0o644); err != nil {
		t.Fatal(err)
	}
	AppConfig = defaultConfig()
	if _, ok := ProbeIdentityFor(ProbeClientClaudeCode); ok {
		t.Fatal("defaults must carry no probe identity")
	}
	loadConfigFile(path)
	identity, ok := ProbeIdentityFor(ProbeClientClaudeCode)
	if !ok || identity.UserAgent != "client/1.0" || identity.Headers["x-app"] != "cli" || identity.Metadata != testProbeMetadata {
		t.Fatalf("identity = %+v, %t", identity, ok)
	}
	if _, err := RenderProbeMetadata(identity.Metadata, 1); err != nil {
		t.Fatal(err)
	}

	AppConfig = defaultConfig()
	loadConfigFile(filepath.Join("..", "..", "config", "backend", "config.yaml"))
	if _, ok := ProbeIdentityFor(ProbeClientClaudeCode); ok {
		t.Fatal("the public config template must not configure a probe identity")
	}
}

// Guard the public template: no client User-Agent string may be baked in.
func TestPublicConfigTemplateHasNoClientUserAgent(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "backend", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile(`(?i)claude-cli/\d`).Match(raw) {
		t.Fatal("config.yaml must not carry a client User-Agent")
	}
}
