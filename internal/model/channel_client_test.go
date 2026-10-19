package model

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
)

func TestClientRestrictedCandidates(t *testing.T) {
	settings := `{"codex_client_only":true}`
	restricted := &Channel{Id: 1, OtherSettings: settings}
	normal := &Channel{Id: 2}
	candidates := []RoutingCandidate{{Channel: restricted, Priority: 100}, {Channel: normal, Priority: 1}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	for _, ctx := range []*gin.Context{nil, c} {
		got := filterClientCandidates(ctx, candidates)
		if len(got) != 1 || got[0].Channel.Id != 2 {
			t.Fatal("generic client can select restricted source")
		}
	}
	c.Request.Header.Set("User-Agent", "Codex Desktop/0.147.0")
	c.Request.Header.Set("originator", "Codex Desktop")
	if len(filterClientCandidates(c, candidates)) != 2 {
		t.Fatal("Codex source missing")
	}
	if len(candidates) != 2 || candidates[0].Channel.Id != 1 {
		t.Fatal("shared candidates mutated")
	}
}

func TestLegacySelectionSkipsRestrictedHigherPriority(t *testing.T) {
	oldEnabled := common.MemoryCacheEnabled
	oldChannels := channelsIDM
	oldAbilities := group2model2channels
	defer func() {
		common.MemoryCacheEnabled = oldEnabled
		channelsIDM = oldChannels
		group2model2channels = oldAbilities
	}()
	common.MemoryCacheEnabled = true
	settings := `{"codex_client_only":true}`
	channelsIDM = map[int]*Channel{1: {Id: 1, Status: common.ChannelStatusEnabled, OtherSettings: settings}, 2: {Id: 2, Status: common.ChannelStatusEnabled}}
	group2model2channels = map[string]map[string][]cachedAbility{"cheap": {"example": {{ChannelID: 1, Priority: 100}, {ChannelID: 2, Priority: 0}}}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	got, _, err := CacheGetRandomSatisfiedChannel(c, "cheap", "example", 0)
	if err != nil || got == nil || got.Id != 2 {
		t.Fatalf("generic selection: %v %v", got, err)
	}
	c.Request.Header.Set("User-Agent", "Codex Desktop/0.147.0")
	c.Request.Header.Set("originator", "Codex Desktop")
	got, _, err = CacheGetRandomSatisfiedChannel(c, "cheap", "example", 0)
	if err != nil || got == nil || got.Id != 1 {
		t.Fatalf("Codex selection: %v %v", got, err)
	}
}

// A lane whose only channels take one client says so, and an unrestricted one
// does not. That pair is what the connect dialog reads to decide whether a
// model can offer the agent lane at all.
func TestGetLaneServiceabilityReportsClientRestrictedLanes(t *testing.T) {
	oldEnabled := common.MemoryCacheEnabled
	oldChannels := channelsIDM
	oldAbilities := group2model2channels
	defer func() {
		common.MemoryCacheEnabled = oldEnabled
		channelsIDM = oldChannels
		group2model2channels = oldAbilities
	}()
	common.MemoryCacheEnabled = true
	channelsIDM = map[int]*Channel{
		1: {Id: 1, Status: common.ChannelStatusEnabled},
		2: {Id: 2, Status: common.ChannelStatusEnabled, OtherSettings: `{"codex_client_only":true}`},
	}
	group2model2channels = map[string]map[string][]cachedAbility{
		ChannelLaneDirect: {"lane-probe": {{ChannelID: 1}}},
		ChannelLaneAgent:  {"lane-probe": {{ChannelID: 2}}},
	}

	state := GetLaneServiceability()["lane-probe"]
	if !state[ChannelLaneDirect].Available || state[ChannelLaneDirect].ClientOnly != "" {
		t.Fatalf("an unrestricted lane must be plainly available: %+v", state[ChannelLaneDirect])
	}
	if !state[ChannelLaneAgent].Available || state[ChannelLaneAgent].ClientOnly != ClientOnlyCodex {
		t.Fatalf("a Codex-only lane must be available and named: %+v", state[ChannelLaneAgent])
	}
	if _, ok := GetLaneServiceability()["model-nobody-serves"]; ok {
		t.Fatal("a model with no channel must not appear at all")
	}

	// One unrestricted channel is enough to open the lane to every client.
	group2model2channels[ChannelLaneAgent]["lane-probe"] = []cachedAbility{{ChannelID: 2}, {ChannelID: 1}}
	if GetLaneServiceability()["lane-probe"][ChannelLaneAgent].ClientOnly != "" {
		t.Fatal("a lane with an open channel must not be reported as client-only")
	}
}

// A Claude Code-only source is selected for Claude Code and skipped for every
// other client, including Codex.
func TestClaudeCodeRestrictedCandidates(t *testing.T) {
	restricted := &Channel{Id: 1, OtherSettings: `{"claude_code_client_only":true}`}
	normal := &Channel{Id: 2}
	candidates := []RoutingCandidate{{Channel: restricted, Priority: 100}, {Channel: normal, Priority: 1}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	for _, ua := range []string{"", "OpenAI/Python", "Codex Desktop/0.147.0", "claude-code-router/1.0"} {
		c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
		c.Request.Header.Set("User-Agent", ua)
		c.Request.Header.Set("originator", "Codex Desktop")
		if got := filterClientCandidates(c, candidates); len(got) != 1 || got[0].Channel.Id != 2 {
			t.Fatalf("%q can select a Claude Code-only source", ua)
		}
	}
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.276 (external, cli)")
	if len(filterClientCandidates(c, candidates)) != 2 {
		t.Fatal("Claude Code source missing for Claude Code")
	}
}

// A lane served only by Claude Code-only sources is named for that client; a
// lane mixing two different restricted clients is not locked to either.
func TestGetLaneServiceabilityReportsClaudeCodeLanes(t *testing.T) {
	oldEnabled := common.MemoryCacheEnabled
	oldChannels := channelsIDM
	oldAbilities := group2model2channels
	defer func() {
		common.MemoryCacheEnabled = oldEnabled
		channelsIDM = oldChannels
		group2model2channels = oldAbilities
	}()
	common.MemoryCacheEnabled = true
	channelsIDM = map[int]*Channel{
		1: {Id: 1, Status: common.ChannelStatusEnabled, OtherSettings: `{"claude_code_client_only":true}`},
		2: {Id: 2, Status: common.ChannelStatusEnabled, OtherSettings: `{"codex_client_only":true}`},
	}
	group2model2channels = map[string]map[string][]cachedAbility{
		ChannelLaneAgent: {"lane-probe": {{ChannelID: 1}}},
	}
	if got := GetLaneServiceability()["lane-probe"][ChannelLaneAgent].ClientOnly; got != ClientOnlyClaudeCode {
		t.Fatalf("a Claude Code-only lane must be named, got %q", got)
	}
	group2model2channels[ChannelLaneAgent]["lane-probe"] = []cachedAbility{{ChannelID: 1}, {ChannelID: 2}}
	if got := GetLaneServiceability()["lane-probe"][ChannelLaneAgent].ClientOnly; got != "" {
		t.Fatalf("a lane split between two clients is not locked to one, got %q", got)
	}
}
