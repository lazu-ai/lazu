package common

import "testing"

func TestParseClient(t *testing.T) {
	cases := []struct {
		name      string
		ua        string
		framework string
		version   string
		language  string
	}{

		// Agentic CLIs.
		{"claude-code with version", "Claude-Code/0.5.2", "claude-code", "0.5.2", "node"},
		{"claude code with space", "Claude Code 1.2.3", "claude-code", "1.2.3", "node"},
		{"codex cli", "Codex-CLI/0.1.0", "codex", "0.1.0", "rust"},
		{"codex rs", "codex_cli_rs/0.140.0 (Mac OS 15.4.0; arm64) ghostty", "codex", "0.140.0", "rust"},
		{"codex tui", "codex-tui/0.141.2 (Ubuntu 24.4.0; x86_64) gnome-terminal (codex-tui; 0.141.2)", "codex", "0.141.2", "rust"},
		{"claude code cli", "claude-cli/2.1.276 (external, cli)", "claude-code", "2.1.276", "node"},
		{"opencode", "opencode/1.4.2", "opencode", "1.4.2", "node"},
		{"pi", "pi (linux 6.8.0-generic; arm64)", "pi", "", "node"},
		{"pi versioned", "pi/0.84.3 (darwin; node/22.1.0; arm64)", "pi", "0.84.3", "node"},
		{"openai js", "OpenAI/JS 5.12.0", "openai-node", "5.12.0", "node"},
		{"anthropic js", "Anthropic/JS 0.60.0", "anthropic-typescript", "0.60.0", "node"},
		{"crush", "Charm-Crush/v0.12.0 (https://charm.land/crush)", "crush", "v0.12.0", "go"},
		{"cursor", "Cursor/0.42.0 (mac)", "cursor", "0.42.0", "node"},
		{"continue", "Continue.dev/0.8.5", "continue", "0.8.5", "node"},
		{"aider", "Aider/0.55.0 (linux)", "aider", "0.55.0", "python"},
		{"gemini cli", "gemini-cli/0.3.0", "gemini-cli", "0.3.0", "node"},

		// SDKs.
		{"openai python", "OpenAI/Python 1.51.0", "openai-python", "1.51.0", "python"},
		{"anthropic python", "anthropic-sdk-python/0.34.2 Python/3.11", "anthropic-python", "0.34.2", "python"},
		{"anthropic ts via ai sdk", "anthropic-ai/sdk/0.27.0", "anthropic-typescript", "0.27.0", "node"},
		{"langchain", "LangChain/0.2.5 python-requests/2.32.0", "langchain", "0.2.5", "python"},

		// HTTP tooling.
		{"curl", "curl/8.4.0", "curl", "8.4.0", "shell"},
		{"axios", "axios/1.6.0", "axios", "1.6.0", "node"},
		{"python-requests", "python-requests/2.32.0", "python-requests", "2.32.0", "python"},
		{"go-http-client", "Go-http-client/1.1", "go-http", "1.1", "go"},
		{"reqwest", "reqwest/0.11.20", "reqwest", "0.11.20", "rust"},

		// Browsers (substring fallback, no version capture).
		{"chrome", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36", "browser-chrome", "", "browser"},

		// Empty / unknown.
		{"empty", "", "", "", ""},
		{"unknown", "MyCustomClient/1.0", "", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseClient(tc.ua)
			if got.Framework != tc.framework {
				t.Errorf("framework: got %q want %q", got.Framework, tc.framework)
			}
			if got.Version != tc.version {
				t.Errorf("version: got %q want %q", got.Version, tc.version)
			}
			if got.Language != tc.language {
				t.Errorf("language: got %q want %q", got.Language, tc.language)
			}
		})
	}
}
