package dto

import (
	"encoding/json"
	"testing"
)

func boolPtr(b bool) *bool { return &b }

// nilCapsAreFullyPermissive locks in the migration-compat promise: every
// channel that existed before ChannelCapabilities was introduced reads
// back as `Capabilities == nil`, and every capability question must
// answer "yes" for them so routing behaviour stays unchanged.
func TestNilCapabilitiesAssumeFullyCapable(t *testing.T) {
	var c *ChannelCapabilities
	if !c.HasChatCompletionsEndpoint() || !c.HasResponsesEndpoint() {
		t.Fatal("nil endpoint flags should preserve dialect defaults")
	}
	if !c.HasTools() {
		t.Fatal("nil HasTools should be true (compat)")
	}
	if !c.HasVision() {
		t.Fatal("nil HasVision should be true (compat)")
	}
	if !c.HasAudio() {
		t.Fatal("nil HasAudio should be true (compat)")
	}
	if !c.FitsContext(1_000_000) {
		t.Fatal("nil FitsContext should be true (compat)")
	}
	if c.PrefersIntent("code") {
		t.Fatal("nil PrefersIntent should be false (no declared preferences)")
	}
}

func TestExplicitFalseEndpointFlagsAreHonoured(t *testing.T) {
	c := &ChannelCapabilities{
		SupportsChatCompletions: boolPtr(false),
		SupportsResponses:       boolPtr(false),
	}
	if c.HasChatCompletionsEndpoint() || c.HasResponsesEndpoint() {
		t.Fatal("explicit false endpoint capability should disable the endpoint")
	}
}

func TestExplicitFalseFlagsAreHonoured(t *testing.T) {
	c := &ChannelCapabilities{
		SupportsTools:  boolPtr(false),
		SupportsVision: boolPtr(false),
		SupportsAudio:  boolPtr(false),
	}
	if c.HasTools() {
		t.Fatal("explicit *false SupportsTools should disable")
	}
	if c.HasVision() {
		t.Fatal("explicit *false SupportsVision should disable")
	}
	if c.HasAudio() {
		t.Fatal("explicit *false SupportsAudio should disable")
	}
}

func TestNilFlagsButNonNilStructStillAssumeYes(t *testing.T) {
	// Empty struct: operator declared *something* (e.g. ContextWindow)
	// but left flags blank → still assume capable.
	c := &ChannelCapabilities{ContextWindow: 128_000}
	if !c.HasTools() || !c.HasVision() || !c.HasAudio() {
		t.Fatal("nil flags on non-nil caps should assume yes")
	}
}

func TestFitsContext(t *testing.T) {
	c := &ChannelCapabilities{ContextWindow: 8192}
	if !c.FitsContext(8192) {
		t.Fatal("exactly-at-window should fit")
	}
	if !c.FitsContext(100) {
		t.Fatal("under-window should fit")
	}
	if c.FitsContext(8193) {
		t.Fatal("over-window should not fit")
	}
	// Zero context_window is treated as "unknown / no limit".
	c0 := &ChannelCapabilities{}
	if !c0.FitsContext(1_000_000) {
		t.Fatal("zero ContextWindow should mean 'no limit'")
	}
}

func TestPrefersIntent(t *testing.T) {
	c := &ChannelCapabilities{PreferredFor: []string{"code", "long_context"}}
	if !c.PrefersIntent("code") {
		t.Fatal("declared intent should match")
	}
	if !c.PrefersIntent("long_context") {
		t.Fatal("declared intent should match")
	}
	if c.PrefersIntent("vision") {
		t.Fatal("undeclared intent should not match")
	}
	if c.PrefersIntent("") {
		t.Fatal("empty intent should never match")
	}
}

func TestCapabilitiesRoundTripsThroughOtherSettings(t *testing.T) {
	// Verify that the new field serializes/deserializes through the same
	// OtherSettings JSON the Channel model already stores. This protects
	// against accidentally introducing schema-breaking changes when the
	// admin console writes Capabilities back.
	original := ChannelOtherSettings{
		AzureResponsesVersion:  "preview",
		ExplicitStreamRequired: true,
		Capabilities: &ChannelCapabilities{
			ContextWindow:   128_000,
			MaxOutputTokens: 4096,
			SupportsTools:   boolPtr(true),
			SupportsVision:  boolPtr(false),
			RPMLimit:        600,
			TPMLimit:        90_000,
			MaxParallel:     50,
			PreferredFor:    []string{"code", "tools"},
		},
	}
	raw, err := json.Marshal(&original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var roundTrip ChannelOtherSettings
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !roundTrip.ExplicitStreamRequired {
		t.Fatal("ExplicitStreamRequired lost in round-trip")
	}
	rc := roundTrip.GetCapabilities()
	if rc == nil {
		t.Fatal("capabilities lost in round-trip")
	}
	if rc.ContextWindow != 128_000 {
		t.Fatalf("ContextWindow=%d", rc.ContextWindow)
	}
	if !rc.HasTools() || rc.HasVision() {
		t.Fatalf("flags wrong: tools=%v vision=%v", rc.HasTools(), rc.HasVision())
	}
	if !rc.PrefersIntent("code") {
		t.Fatal("PreferredFor lost in round-trip")
	}
}

func TestCapabilitiesAbsentFromExistingPayload(t *testing.T) {
	// An OtherSettings JSON from before this field existed must unmarshal
	// without error and produce nil Capabilities.
	raw := []byte(`{"azure_responses_version":"preview","allow_service_tier":true}`)
	var settings ChannelOtherSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("legacy payload unmarshal: %v", err)
	}
	if settings.GetCapabilities() != nil {
		t.Fatal("legacy payload should produce nil Capabilities")
	}
}

func TestGetCapabilitiesOnNilReceiver(t *testing.T) {
	var s *ChannelOtherSettings
	if s.GetCapabilities() != nil {
		t.Fatal("nil receiver should produce nil capabilities (no panic)")
	}
}
