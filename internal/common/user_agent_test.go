package common

import "testing"

// Every agent string here was read from the client's source or an official
// capture on 2026-10-29; the SDK and HTTP strings follow each library's
// documented format.
func TestParseClient(t *testing.T) {
	cases := []struct {
		name      string
		ua        string
		framework string
		version   string
		language  string
	}{
		// Coding agents.
		{"claude code cli", "claude-cli/2.1.276 (external, cli)", "claude-code", "2.1.276", "node"},
		{"claude code sdk", "claude-cli/2.1.141 (external, sdk-cli)", "claude-code", "2.1.141", "node"},
		{"claude code sign-in client", "claude-code/2.1.247", "claude-code", "2.1.247", "node"},
		{"claude code router side call is not claude code", "Claude-Code-Router/provider-manifest", "", "", ""},
		{"codex rs", "codex_cli_rs/0.140.0 (Mac OS 15.4.0; arm64) ghostty", "codex", "0.140.0", "rust"},
		{"codex tui", "codex-tui/0.166.0 (Mac OS 26.2.0; arm64) iTerm.app/3.6.6 (codex-tui; 0.166.0)", "codex", "0.166.0", "rust"},
		{"codex exec", "codex_exec/0.165.0 (Ubuntu 24.4.0; x86_64)", "codex", "0.165.0", "rust"},
		{"codex desktop", "Codex Desktop", "codex", "", "rust"},
		{"hermes on a codex host", "codex_cli_rs/0.0.0 (Hermes Agent)", "hermes-agent", "", "python"},
		{"hermes native", "HermesAgent/0.22.5", "hermes-agent", "0.22.5", "python"},
		{"opencode v1", "opencode/latest/1.19.5/cli ai-sdk-provider-utils/4.0.58 bun/1.3.5", "opencode", "1.19.5", "node"},
		{"opencode v2", "opencode/latest/2.2.1/opencode", "opencode", "2.2.1", "node"},
		{"opencode legacy", "opencode/1.4.2", "opencode", "1.4.2", "node"},
		{"kilo code", "Kilo-Code/7.8.25 ai-sdk/openai-compatible/2.0.48 ai-sdk/provider-utils/4.0.15 runtime/node.js/22", "kilo-code", "7.8.25", "node"},
		{"kilo code anthropic provider", "kilo/latest/7.8.25/cli ai-sdk/anthropic/3.0.111 ai-sdk/provider-utils/4.0.15 runtime/node.js/22", "kilo-code", "7.8.25", "node"},
		{"kilo code legacy", "kilo-code/5.1.0 (darwin 25.2.0; arm64) node/22.21.1", "kilo-code", "5.1.0", "node"},
		{"roo code openai compatible", "RooCode/3.53.0", "roo-code", "3.53.0", "node"},
		{"roo code native openai", "roo-code/3.45.0 (darwin 25.2.0; arm64) node/22.21.1", "roo-code", "3.45.0", "node"},
		{"crush", "Charm-Crush/v0.98.2 (https://charm.land/crush)", "crush", "v0.98.2", "go"},
		{"openclaw native", "openclaw/2026.10.8", "openclaw", "2026.10.8", "node"},
		{"openclaw chatgpt", "openclaw (darwin 25.2.0; arm64)", "openclaw", "", "node"},
		{"pi", "pi/1.4.0 (darwin; node/v22.12.0; arm64)", "pi", "1.4.0", "node"},
		{"pi early", "pi (linux 6.8.0-generic; arm64)", "pi", "", "node"},
		{"gemini cli tui", "GeminiCLI-tui/0.66.0/gemini-2.5-pro (darwin; arm64; terminal)", "gemini-cli", "0.66.0", "node"},
		{"gemini cli acp", "GeminiCLI-acp-zed/0.66.0/gemini-2.5-flash (linux; x64; terminal)", "gemini-cli", "0.66.0", "node"},
		{"gemini cli vscode", "CloudCodeVSCode/1.2.3 (aidev_client; os_type=darwin; proxy_client=geminicli)", "gemini-cli", "1.2.3", "node"},
		{"gemini cli early", "gemini-cli/0.3.0", "gemini-cli", "0.3.0", "node"},
		{"qwen code", "QwenCode/0.26.4 (darwin; arm64)", "qwen-code", "0.26.4", "node"},
		{"kimi cli", "KimiCLI/1.52.0", "kimi-cli", "1.52.0", "python"},
		{"iflow cli", "iFlow-Cli", "iflow-cli", "", "node"},
		{"iflow cli versioned", "iFlowCLI/0.5.19 (darwin; arm64)", "iflow-cli", "0.5.19", "node"},
		{"copilot byok", "GitHubCopilotChat/0.73.0", "github-copilot", "0.73.0", "node"},
		{"jan agent", "Jan-Agent/0.7.2 (macos; aarch64)", "jan", "0.7.2", "rust"},
		{"cline v3", "Cline/3.89.2", "cline", "3.89.2", "node"},
		{"zed", "Zed/0.219.4+stable.119.abc1234 (macos; aarch64)", "zed", "0.219.4", "rust"},
		{"continue clawrouter", "Continue/IDE", "continue", "", "node"},
		{"cc switch", "cc-switch/4.0.21", "cc-switch", "4.0.21", "rust"},

		// Apps.
		{"anythingllm", "AnythingLLM/1.19.0", "anythingllm", "1.19.0", "node"},
		{"lobechat anthropic", "lobehub/2.2.21", "lobechat", "2.2.21", "node"},
		{"cherry studio v1", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) CherryStudio/1.9.13 Chrome/136.0.7103.149 Electron/36.4.0 Safari/537.36", "cherry-studio", "1.9.13", "browser"},
		{"chatbox", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) xyz.chatboxapp.ce/1.16.2 Chrome/134.0.6998.205 Electron/35.2.1 Safari/537.36", "chatbox", "1.16.2", "browser"},
		{"openai agents python", "Agents/Python 0.24.0", "openai-agents", "0.24.0", "python"},
		{"openai agents js", "Agents/JavaScript 0.21.0", "openai-agents", "0.21.0", "python"},
		{"mastra", "mastra/0.14.0 ai-sdk/openai/3.0.134 ai-sdk/provider-utils/4.0.15 runtime/node.js/22", "mastra", "0.14.0", "node"},

		// SDKs.
		{"openai python", "OpenAI/Python 1.51.0", "openai-python", "1.51.0", "python"},
		{"openai python async", "AsyncOpenAI/Python 3.41.0", "openai-python", "3.41.0", "python"},
		{"openai js", "OpenAI/JS 5.12.0", "openai-node", "5.12.0", "node"},
		{"openai js subclass", "OpenAICompatibleClient/JS 7.45.1", "openai-node", "7.45.1", "node"},
		{"openai go", "OpenAI/Go 3.90.0", "openai-go", "3.90.0", "go"},
		{"openai java", "OpenAIClientImpl/Java 4.94.0", "openai-java", "4.94.0", "java"},
		{"openai dotnet", "OpenAI/2.14.0 (.NET 9.0.1; Darwin 25.2.0 Darwin Kernel Version 25.2.0)", "openai-dotnet", "2.14.0", "dotnet"},
		{"anthropic js", "Anthropic/JS 0.60.0", "anthropic-typescript", "0.60.0", "node"},
		{"anthropic python", "Anthropic/Python 2.3.0", "anthropic-python", "2.3.0", "python"},
		{"anthropic python async", "AsyncAnthropic/Python 0.78.0", "anthropic-python", "0.78.0", "python"},
		{"anthropic python legacy", "anthropic-sdk-python/0.34.2 Python/3.11", "anthropic-python", "0.34.2", "python"},
		{"anthropic go", "Anthropic/Go 2.4.0", "anthropic-go", "2.4.0", "go"},
		{"anthropic java", "AnthropicClientAsyncImpl/Java 3.3.0", "anthropic-java", "3.3.0", "java"},
		{"anthropic ts via ai sdk", "anthropic-ai/sdk/0.27.0", "anthropic-typescript", "0.27.0", "node"},
		{"minified stainless js", "Fn/JS 6.9.0", "stainless-js", "6.9.0", "node"},
		{"minified stainless js anthropic", "XE/JS 0.51.0", "stainless-js", "0.51.0", "node"},
		{"google genai", "google-genai-sdk/2.34.0 gl-python/3.12.4", "google-genai", "2.34.0", "python"},
		{"langchain js", "langchainjs-openai/1.0.0 ((node/v22.12.0; linux; x64))", "langchain", "1.0.0", "python"},
		{"langchain anthropic", "langchain-anthropic/1.7.7", "langchain", "1.7.7", "python"},
		{"langchain python", "LangChain/0.2.5 python-requests/2.32.0", "langchain", "0.2.5", "python"},
		{"litellm", "litellm/1.82.3", "litellm", "1.82.3", "python"},
		{"llama index", "llama-index/0.14.2", "llama-index", "0.14.2", "python"},
		{"semantic kernel python", "semantic-kernel-python/1.38.0 AsyncOpenAI/Python 2.1.0", "semantic-kernel", "1.38.0", "python"},
		{"spring ai", "spring-ai-openai", "spring-ai", "", "java"},
		{"vercel ai sdk v6", "ai-sdk/openai-compatible/2.0.72 ai-sdk/provider-utils/4.0.58 runtime/node.js/22", "ai-sdk", "2.0.72", "node"},
		{"vercel ai sdk v7", "ai-sdk-openai/4.0.104 ai-sdk-provider-utils/5.0.2 node.js/22", "ai-sdk", "4.0.104", "node"},

		// HTTP tooling.
		{"curl", "curl/8.4.0", "curl", "8.4.0", "shell"},
		{"axios", "axios/1.6.0", "axios", "1.6.0", "node"},
		{"node-fetch", "node-fetch", "node-fetch", "", "node"},
		{"undici", "undici", "node-fetch", "", "node"},
		{"bun", "Bun/1.3.5", "bun", "1.3.5", "node"},
		{"python-requests", "python-requests/2.32.0", "python-requests", "2.32.0", "python"},
		{"python-httpx", "python-httpx/0.27.0", "python-httpx", "0.27.0", "python"},
		{"aiohttp", "Python/3.12 aiohttp/3.9.5", "aiohttp", "3.9.5", "python"},
		{"go-http-client", "Go-http-client/1.1", "go-http", "1.1", "go"},
		{"reqwest", "reqwest/0.11.20", "reqwest", "0.11.20", "rust"},
		{"java http client", "Java-http-client/21.0.2", "java-http", "21.0.2", "java"},
		{"ktor", "ktor-client", "ktor", "", "java"},
		{"postman", "PostmanRuntime/7.36.0", "postman", "7.36.0", "browser"},

		// Browsers and Electron (substring fallback, no version capture).
		{"chrome", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36", "browser-chrome", "", "browser"},
		{"unknown electron app", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) SomeApp/2.0 Chrome/136.0.0.0 Electron/36.4.0 Safari/537.36", "electron", "36.4.0", "browser"},

		// Substrings of agent names inside other words are not agents.
		{"zed inside a word", "optimized-client/1.0", "", "", ""},
		{"cline inside a word", "decline-bot/2.0", "", "", ""},
		{"cursor inside a word", "precursor/0.1", "", "", ""},

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

func TestParseClientRequestHeaders(t *testing.T) {
	cases := []struct {
		name      string
		headers   ClientHeaders
		framework string
	}{
		{"codex originator over sdk ua", ClientHeaders{UserAgent: "OpenAI/JS 7.39.0", Originator: "codex_cli_rs"}, "codex"},
		{"codex tui originator with no ua", ClientHeaders{Originator: "codex-tui"}, "codex"},
		{"codex desktop originator", ClientHeaders{UserAgent: "reqwest/0.12.0", Originator: "Codex Desktop"}, "codex"},
		{"hermes ua beats codex originator", ClientHeaders{UserAgent: "codex_cli_rs/0.0.0 (Hermes Agent)", Originator: "codex_cli_rs"}, "hermes-agent"},
		{"roo code originator", ClientHeaders{UserAgent: "OpenAI/JS 5.12.0", Originator: "roo-code"}, "roo-code"},
		{"pi originator on codex path", ClientHeaders{UserAgent: "node-fetch", Originator: "pi"}, "pi"},
		{"openclaw originator", ClientHeaders{UserAgent: "OpenAI/JS 7.39.0", Originator: "openclaw"}, "openclaw"},
		{"originator never overrides a named ua", ClientHeaders{UserAgent: "RooCode/3.53.0", Originator: "codex_cli_rs"}, "roo-code"},
		{"x-app cli with no ua", ClientHeaders{XApp: "cli"}, "claude-code"},
		{"x-app cli-bg", ClientHeaders{UserAgent: "", XApp: "cli-bg"}, "claude-code"},
		{"x-app does not override an sdk ua", ClientHeaders{UserAgent: "Anthropic/JS 0.60.0", XApp: "cli"}, "anthropic-typescript"},
		{"qwen code impersonating claude code", ClientHeaders{UserAgent: "claude-cli/0.26.4 (external, cli)", XApp: "cli"}, "qwen-code"},
		{"real claude code keeps its tag", ClientHeaders{UserAgent: "claude-cli/2.1.276 (external, cli)", XApp: "cli"}, "claude-code"},
		{"cherry studio v1 x-title", ClientHeaders{UserAgent: "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36", XTitle: "Cherry Studio"}, "cherry-studio"},
		{"x-title names an sdk caller", ClientHeaders{UserAgent: "python-requests/2.32.0", XTitle: "Open WebUI"}, "open-webui"},
		{"x-title alias", ClientHeaders{UserAgent: "node-fetch", XTitle: "Chatbox AI"}, "chatbox"},
		{"x-title never overrides a named ua", ClientHeaders{UserAgent: "Kilo-Code/7.8.25", XTitle: "Kilo Code"}, "kilo-code"},
		{"x-title too short is ignored", ClientHeaders{UserAgent: "curl/8.4.0", XTitle: "x"}, "curl"},
		{"x-title spelling variants", ClientHeaders{UserAgent: "curl/8.4.0", XTitle: "  cherry_studio "}, "cherry-studio"},
		{"x-title outside the allowlist is ignored", ClientHeaders{UserAgent: "curl/8.4.0", XTitle: "My App"}, "curl"},
		{"originator outside the allowlist is ignored", ClientHeaders{UserAgent: "curl/8.4.0", Originator: "my-agent"}, "curl"},
		{"nothing", ClientHeaders{UserAgent: "MyCustomClient/1.0"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseClientRequest(tc.headers).Framework; got != tc.framework {
				t.Errorf("framework: got %q want %q", got, tc.framework)
			}
		})
	}
}

// A client named by a header never inherits the version of the SDK,
// runtime or Electron shell its User-Agent describes; one the User-Agent
// names keeps its own.
func TestParseClientRequestVersion(t *testing.T) {
	cases := []struct {
		name    string
		headers ClientHeaders
		version string
	}{
		{"cherry studio via x-title drops the electron version", ClientHeaders{UserAgent: "Mozilla/5.0 (Macintosh) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Electron/36.4.0 Safari/537.36", XTitle: "Cherry Studio"}, ""},
		{"codex via originator drops the sdk version", ClientHeaders{UserAgent: "OpenAI/JS 7.39.0", Originator: "codex_cli_rs"}, ""},
		{"roo code via originator drops the sdk version", ClientHeaders{UserAgent: "OpenAI/JS 5.12.0", Originator: "roo-code"}, ""},
		{"named user-agent keeps its version", ClientHeaders{UserAgent: "RooCode/3.53.0", Originator: "codex_cli_rs"}, "3.53.0"},
		{"qwen code keeps its own version", ClientHeaders{UserAgent: "claude-cli/0.26.4 (external, cli)", XApp: "cli"}, "0.26.4"},
		{"library keeps its version when nothing names a client", ClientHeaders{UserAgent: "OpenAI/Python 1.51.0"}, "1.51.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseClientRequest(tc.headers).Version; got != tc.version {
				t.Errorf("version: got %q want %q", got, tc.version)
			}
		})
	}
}

func TestClientClassification(t *testing.T) {
	for _, tag := range []string{"claude-code", "codex", "opencode", "kilo-code", "goose", "cherry-studio", "github-copilot"} {
		if !IsAgentClient(tag) || !IsNamedClient(tag) || IsLibraryClient(tag) {
			t.Errorf("%s should be a named agent", tag)
		}
	}
	for _, tag := range []string{"openai-python", "anthropic-typescript", "ai-sdk", "curl", "browser-chrome", "electron", "stainless-js"} {
		if !IsLibraryClient(tag) || IsNamedClient(tag) || IsAgentClient(tag) {
			t.Errorf("%s should be a library", tag)
		}
	}
	// Header-only names count as named; anything else does not.
	if !IsNamedClient("open-webui") {
		t.Errorf("header allowlist names are named")
	}
	for _, tag := range []string{"", "my-app", "MyCustomClient"} {
		if IsNamedClient(tag) {
			t.Errorf("%q is not named", tag)
		}
	}
	// Every agent and app in the table has a display name.
	for tag := range namedClients {
		if _, ok := clientDisplayNames[tag]; !ok {
			t.Errorf("%s has no display name", tag)
		}
	}
	if got := ClientDisplayName("my-app"); got != "my-app" {
		t.Errorf("unknown tags display as themselves, got %q", got)
	}
}
