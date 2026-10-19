package model

import "testing"

func TestChannelCompatibilitySettingsValidation(t *testing.T) {
	for _, tc := range []struct {
		settings string
		valid    bool
	}{
		{`{}`, true},
		{`{"claude_omit_unused_system_beta_models":[]}`, true},
		{`{"claude_omit_unused_system_beta_models":["claude-sonnet-5"]}`, true},
		{`{"claude_omit_unused_system_beta_models":["claude-*"]}`, false},
		{`{"claude_omit_unused_system_beta_models":[" claude-sonnet-5"]}`, false},
		{`{"claude_omit_unused_system_beta_models":["claude-sonnet-5","claude-sonnet-5"]}`, false},
		{`{"claude_omit_unused_system_beta_models":"claude-sonnet-5"}`, false},
		{`{"claude_omit_unused_cache_scope_beta_models":["claude-sonnet-5","claude-opus-5"]}`, true},
		{`{"claude_omit_unused_cache_scope_beta_models":["claude-*"]}`, false},
		{`{"claude_omit_keep_all_context_management_models":["claude-sonnet-5"]}`, true},
		{`{"claude_omit_keep_all_context_management_models":["claude-sonnet-5","claude-sonnet-5"]}`, false},
		{`{"claude_fold_system_turns_models":["claude-opus-5"],"claude_omit_thinking_token_count_beta_models":["claude-opus-5"]}`, true},
		{`{"claude_fold_system_turns_models":["claude-*"]}`, false},
		{`{"claude_fold_system_turns_models":["claude-opus-5"],"claude_omit_unused_system_beta_models":["claude-opus-5"]}`, false},
	} {
		channel := Channel{OtherSettings: tc.settings}
		if err := channel.ValidateSettings(); (err == nil) != tc.valid {
			t.Errorf("settings=%s error=%v", tc.settings, err)
		}
	}
}
