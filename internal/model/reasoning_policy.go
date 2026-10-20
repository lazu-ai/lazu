package model

import (
	"regexp"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

// How a model takes its thinking controls on Anthropic's Messages API.
//
// The catalog row (Model.ReasoningOptions) is the only source the relay path
// reads: ReasoningPolicy looks it up and nothing else. The seed table below
// exists to fill blank rows, at the one-time startup backfill and when a
// model is registered from models.dev, which knows nothing of these shapes.
// It is never consulted while serving a request; a model the catalog does
// not describe is served as it was before Mode and Off existed.
//
// The table's content comes from Anthropic's own refusals as magpie
// recorded them (github.com/yetone/magpie, internal/gateway/anthropic.go:
// adaptiveOnly, thinkingOffOf), checked against platform.claude.com's
// extended-thinking and effort pages on 2026-10-20.

const (
	// ReasoningModeBudget: thinking.type=enabled with budget_tokens, every
	// Claude before 4.6.
	ReasoningModeBudget = "budget"
	// ReasoningModeAdaptive: thinking.type=adaptive with the effort in
	// output_config.effort; Claude 4.6 and later refuse a budget
	// ("requires adaptive thinking").
	ReasoningModeAdaptive = "adaptive"

	// ReasoningOffDisabled: thinking.type=disabled is taken at any effort.
	ReasoningOffDisabled = "disabled"
	// ReasoningOffUpToHigh: disabled is taken at effort high or below;
	// xhigh and max refuse it (Opus 5, Haiku 5.5).
	ReasoningOffUpToHigh = "disabled_up_to_high"
	// ReasoningOffBetweenTools: disabled is refused and the model asks for
	// thinking.type=between_tools, at effort high or below (Sonnet 5.5).
	ReasoningOffBetweenTools = "between_tools"
	// ReasoningOffNever: thinking cannot be turned off; the field is left
	// out and the model thinks adaptively (Opus 5.5, Fable 5, Mythos 5).
	ReasoningOffNever = "never"
)

// ReasoningPolicy is the catalog's reasoning options for the first of names
// it describes, zero when it describes none. Callers pass the model as the
// client named it and then as the channel maps it: the catalog row is on
// the public name.
func ReasoningPolicy(names ...string) ModelReasoningOptions {
	for _, name := range names {
		if name == "" {
			continue
		}
		if options, ok := catalogReasoningOptions(name); ok {
			return options
		}
	}
	return ModelReasoningOptions{}
}

// ClaudeThinkingOff is how a request that turns thinking off goes to a model
// with policy: the thinking.type to send ("" leaves the field out) and the
// effort to send with it. requested is the type the client sent, "disabled"
// or "between_tools"; effort is the output_config.effort it asked, "" for
// none. An unknown policy sends the request as it came.
func ClaudeThinkingOff(policy ModelReasoningOptions, requested, effort string) (thinkingType, effortOut string) {
	capHigh := func(e string) string {
		if EffortRank(e) > EffortRank("high") {
			return "high"
		}
		return e
	}
	switch policy.Off {
	case ReasoningOffNever:
		if effort == "" {
			effort = "low"
		}
		return "", effort
	case ReasoningOffBetweenTools:
		return "between_tools", capHigh(effort)
	case ReasoningOffUpToHigh:
		return "disabled", capHigh(effort)
	case ReasoningOffDisabled:
		return "disabled", effort
	}
	return requested, effort
}

// claudeLine reads a Claude model's family and version however a relay or
// cloud spells it: claude-opus-4-7, claude-haiku-5-5-20260301,
// anthropic.claude-sonnet-5-5-v1:0, claude-sonnet-5.5@20261001,
// claude-fable-5-1, and the old order, claude-3-7-sonnet.
var claudeLine = regexp.MustCompile(`(?:^|[^a-z0-9])(?:claude-)?(opus|sonnet|haiku|fable|mythos)-(\d{1,2})(?:[-.](\d{1,2}))?(?:[^0-9]|$)|claude-(\d{1,2})(?:[-.](\d{1,2}))?-(opus|sonnet|haiku)`)

// ClaudeReasoningSeed is the seed table's row for a Claude model name: its
// Mode and, where the table knows it, its Off. ok is false for a name the
// table does not read as a Claude. Only backfills and registration prefill
// call it (see the file comment).
func ClaudeReasoningSeed(modelName string) (seed ModelReasoningOptions, ok bool) {
	m := claudeLine.FindStringSubmatch(strings.ToLower(strings.TrimSpace(modelName)))
	if m == nil {
		return ModelReasoningOptions{}, false
	}
	family, majorText, minorText := m[1], m[2], m[3]
	if family == "" {
		family, majorText, minorText = m[6], m[4], m[5]
	}
	major, _ := strconv.Atoi(majorText)
	minor, _ := strconv.Atoi(minorText)
	seed.Mode = ReasoningModeBudget
	if major > 4 || major == 4 && minor >= 6 {
		seed.Mode = ReasoningModeAdaptive
	}
	switch {
	case family == "fable" || family == "mythos":
		if major == 5 {
			seed.Off = ReasoningOffNever
		}
	case major < 5:
		seed.Off = ReasoningOffDisabled
	case major == 5 && minor == 0:
		seed.Off = ReasoningOffDisabled
		if family == "opus" {
			seed.Off = ReasoningOffUpToHigh
		}
	case major == 5 && minor == 5:
		switch family {
		case "sonnet":
			seed.Off = ReasoningOffBetweenTools
		case "opus":
			seed.Off = ReasoningOffNever
		case "haiku":
			seed.Off = ReasoningOffUpToHigh
		}
	}
	return seed, true
}

// SeedReasoningPolicy is options with its blank Mode and Off filled from the
// seed table for modelName; options itself for a name the table does not
// know or a row that already says both.
func SeedReasoningPolicy(modelName string, options ModelReasoningOptions) ModelReasoningOptions {
	if options.Mode != "" && options.Off != "" {
		return options
	}
	seed, ok := ClaudeReasoningSeed(modelName)
	if !ok {
		return options
	}
	if options.Mode == "" {
		options.Mode = seed.Mode
	}
	if options.Off == "" {
		options.Off = seed.Off
	}
	return options
}

// backfillClaudeReasoningPolicy fills Mode and Off on every catalog row the
// seed table knows and that lacks them. Run once at startup
// (claude_reasoning_policy_v1); rows registered after it are seeded at
// registration.
func backfillClaudeReasoningPolicy(db *gorm.DB) error {
	if db == nil || !db.Migrator().HasTable(&Model{}) {
		return nil
	}
	var rows []Model
	if err := db.Select("id", "model_name", "reasoning_options").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		next := SeedReasoningPolicy(row.ModelName, row.ReasoningOptions)
		if next.Mode == row.ReasoningOptions.Mode && next.Off == row.ReasoningOptions.Off {
			continue
		}
		if err := db.Model(&Model{}).Where("id = ?", row.Id).Update("reasoning_options", next).Error; err != nil {
			return err
		}
	}
	return nil
}

// backfillClaudeReasoningPolicyOnce runs the backfill under its one-time
// marker where the marker table exists, and unrecorded where it does not.
func backfillClaudeReasoningPolicyOnce(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	if !db.Migrator().HasTable(&SchemaVersion{}) {
		return backfillClaudeReasoningPolicy(db)
	}
	return runOneTimeMigration(db, "claude_reasoning_policy_v1", func() error { return backfillClaudeReasoningPolicy(db) })
}
