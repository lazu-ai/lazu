package common

import (
	"regexp"
	"strings"
)

// ClientInfo captures who-sent-the-request so it can be logged alongside
// the usual metadata. We do not try to be comprehensive across every UA
// in the wild — the goal is to recognize the popular agent CLIs and
// official SDKs cleanly, and to keep the raw string for everything else.
type ClientInfo struct {
	// Raw is the verbatim User-Agent header. Always populated when one
	// was sent.
	Raw string
	// Framework is a stable identifier such as "claude-code", "codex",
	// "openai-python", "anthropic-python", "cursor", "continue", "curl".
	// Empty when unrecognized.
	Framework string
	// Version is the framework's reported version, when extractable.
	Version string
	// Language groups frameworks by runtime ("python", "node", "go",
	// "rust", "shell", "browser"). Empty for unrecognized clients.
	Language string
}

// ParseClientRequest is ParseClient plus the headers agents use to name
// themselves when the User-Agent belongs to an SDK: Codex sends
// `originator: codex_cli_rs`, Claude Code sends `x-app: cli`.
func ParseClientRequest(userAgent string, originator string, xApp string) ClientInfo {
	info := ParseClient(userAgent)
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(originator)), "codex") && info.Framework != "codex" {
		info.Framework, info.Language = "codex", "rust"
	}
	if info.Framework == "" {
		switch strings.ToLower(strings.TrimSpace(xApp)) {
		case "cli", "cli-bg":
			info.Framework, info.Language = "claude-code", "node"
		}
	}
	return info
}

// ParseClient inspects the User-Agent and returns a ClientInfo. The
// patterns are ordered most-specific first; the first match wins.
func ParseClient(userAgent string) ClientInfo {
	info := ClientInfo{Raw: strings.TrimSpace(userAgent)}
	if info.Raw == "" {
		return info
	}
	lower := strings.ToLower(info.Raw)

	for _, p := range clientPatterns {
		if p.re != nil {
			if matches := p.re.FindStringSubmatch(info.Raw); matches != nil {
				info.Framework = p.framework
				info.Language = p.language
				if len(matches) > 1 {
					info.Version = matches[1]
				}
				return info
			}
		}
		if p.contains != "" && strings.Contains(lower, p.contains) {
			info.Framework = p.framework
			info.Language = p.language
			return info
		}
	}
	return info
}

type clientPattern struct {
	re        *regexp.Regexp
	contains  string // case-insensitive substring fallback when re has no version
	framework string
	language  string
}

// Patterns are tried in declaration order. Specific agents (Claude Code,
// Codex CLI, IDE extensions) come before generic SDKs so e.g. "Claude
// Code 1.2.3 (anthropic-python 0.34.0)" is attributed to Claude Code.
var clientPatterns = []clientPattern{
	// Agentic CLIs first (most specific). Each agent's own User-Agent, as its
	// source sends it (see memory/reference-agent-session-ids).
	{re: regexp.MustCompile(`(?i)claude-cli/([\d.]+)`), framework: "claude-code", language: "node"},
	{re: regexp.MustCompile(`(?i)Claude[ -]?Code[ /]?([\d.]+)?`), framework: "claude-code", language: "node"},
	{re: regexp.MustCompile(`(?i)codex(?:_cli_rs|-tui|_exec|_vscode|-cli)?[ /]([\d.]+)`), framework: "codex", language: "rust"},
	{re: regexp.MustCompile(`(?i)\bcodex\b`), framework: "codex", language: "rust"},
	{re: regexp.MustCompile(`(?i)Kilo-?Code[ /]?([\d.]+)?`), framework: "kilo-code", language: "node"},
	{re: regexp.MustCompile(`(?i)opencode/([\d.]+)`), framework: "opencode", language: "node"},
	{re: regexp.MustCompile(`(?i)RooCode[ /]?([\d.]+)?`), framework: "roo-code", language: "node"},
	{re: regexp.MustCompile(`(?i)Charm-Crush/([\w.]+)`), framework: "crush", language: "go"},
	{re: regexp.MustCompile(`(?i)openclaw/([\d.]+)`), framework: "openclaw", language: "node"},
	// pi sends "pi (darwin 25.2.0; arm64)" to model APIs, with no version; the
	// versioned "pi/x.y.z (...)" form only goes to pi.dev. Take a version if
	// it ever arrives.
	{re: regexp.MustCompile(`(?i)^pi(?:/([\d.]+))? \(`), framework: "pi", language: "node"},
	{re: regexp.MustCompile(`(?i)GeminiCLI/([\d.]+)`), framework: "gemini-cli", language: "node"},
	{re: regexp.MustCompile(`(?i)QwenCode/([\d.]+)`), framework: "qwen-code", language: "node"},
	{re: regexp.MustCompile(`(?i)KimiCLI/([\d.]+)`), framework: "kimi-cli", language: "python"},
	{re: regexp.MustCompile(`(?i)Cursor[ /]?([\d.]+)?`), framework: "cursor", language: "node"},
	{re: regexp.MustCompile(`(?i)Continue\.?dev[ /]?([\d.]+)?`), framework: "continue", language: "node"},
	{re: regexp.MustCompile(`(?i)Cline[ /]?([\d.]+)?`), framework: "cline", language: "node"},
	{re: regexp.MustCompile(`(?i)Aider[ /]?([\d.]+)?`), framework: "aider", language: "python"},
	{re: regexp.MustCompile(`(?i)Zed[ /]?([\d.]+)?`), framework: "zed", language: "rust"},
	{re: regexp.MustCompile(`(?i)Goose[ /]?([\d.]+)?`), framework: "goose", language: "rust"},
	{re: regexp.MustCompile(`(?i)gemini[- ]cli[ /]?([\d.]+)?`), framework: "gemini-cli", language: "node"},

	// Official SDKs.
	{re: regexp.MustCompile(`(?i)OpenAI/Python\s+([\d.]+)`), framework: "openai-python", language: "python"},
	{re: regexp.MustCompile(`(?i)OpenAI[ /]NodeJS\s+([\d.]+)`), framework: "openai-node", language: "node"},
	{re: regexp.MustCompile(`(?i)OpenAI/JS\s+([\d.]+)`), framework: "openai-node", language: "node"},
	{re: regexp.MustCompile(`(?i)Anthropic/JS\s+([\d.]+)`), framework: "anthropic-typescript", language: "node"},
	{re: regexp.MustCompile(`(?i)Anthropic/Python\s+([\d.]+)`), framework: "anthropic-python", language: "python"},
	{re: regexp.MustCompile(`(?i)anthropic-sdk-python[ /]?([\d.]+)?`), framework: "anthropic-python", language: "python"},
	{re: regexp.MustCompile(`(?i)anthropic-sdk-typescript[ /]?([\d.]+)?`), framework: "anthropic-typescript", language: "node"},
	{re: regexp.MustCompile(`(?i)anthropic-ai/sdk[ /]?([\d.]+)?`), framework: "anthropic-typescript", language: "node"},
	{re: regexp.MustCompile(`(?i)google-generativeai[ /]?([\d.]+)?`), framework: "google-genai-python", language: "python"},
	{re: regexp.MustCompile(`(?i)mistralai[ /]?([\d.]+)?`), framework: "mistral-python", language: "python"},

	// LLM tooling layers.
	{re: regexp.MustCompile(`(?i)LangChain[ /]?([\d.]+)?`), framework: "langchain", language: "python"},
	{re: regexp.MustCompile(`(?i)LiteLLM[ /]?([\d.]+)?`), framework: "litellm", language: "python"},
	{re: regexp.MustCompile(`(?i)LlamaIndex[ /]?([\d.]+)?`), framework: "llama-index", language: "python"},

	// Generic HTTP / runtime.
	{re: regexp.MustCompile(`(?i)curl/([\d.]+)`), framework: "curl", language: "shell"},
	{re: regexp.MustCompile(`(?i)Wget/([\d.]+)`), framework: "wget", language: "shell"},
	{re: regexp.MustCompile(`(?i)httpie[ /]?([\d.]+)?`), framework: "httpie", language: "shell"},
	{re: regexp.MustCompile(`(?i)Postman[ /]?([\d.]+)?`), framework: "postman", language: "browser"},
	{re: regexp.MustCompile(`(?i)Insomnia[ /]?([\d.]+)?`), framework: "insomnia", language: "browser"},
	{re: regexp.MustCompile(`(?i)axios/([\d.]+)`), framework: "axios", language: "node"},
	{re: regexp.MustCompile(`(?i)node-fetch[ /]?([\d.]+)?`), framework: "node-fetch", language: "node"},
	{re: regexp.MustCompile(`(?i)python-requests/([\d.]+)`), framework: "python-requests", language: "python"},
	{re: regexp.MustCompile(`(?i)python-httpx/([\d.]+)`), framework: "python-httpx", language: "python"},
	{re: regexp.MustCompile(`(?i)Python-urllib/([\d.]+)`), framework: "python-urllib", language: "python"},
	{re: regexp.MustCompile(`(?i)okhttp/([\d.]+)`), framework: "okhttp", language: "java"},
	{re: regexp.MustCompile(`(?i)aiohttp/([\d.]+)`), framework: "aiohttp", language: "python"},
	{re: regexp.MustCompile(`(?i)Go-http-client/([\d.]+)`), framework: "go-http", language: "go"},
	{re: regexp.MustCompile(`(?i)reqwest/([\d.]+)`), framework: "reqwest", language: "rust"},

	// Browsers — last resort, distinguishes "user pasted curl into a
	// browser console" from "real desktop browser hitting the API".
	{contains: "chrome/", framework: "browser-chrome", language: "browser"},
	{contains: "firefox/", framework: "browser-firefox", language: "browser"},
	{contains: "safari/", framework: "browser-safari", language: "browser"},
}

var extensionClientNames = map[string]string{}

// RegisterClientPattern recognises an extension's own client by its
// User-Agent ahead of every built-in pattern, and names it in digests.
func RegisterClientPattern(pattern string, framework string, language string, displayName string) {
	clientPatterns = append([]clientPattern{{re: regexp.MustCompile(pattern), framework: framework, language: language}}, clientPatterns...)
	extensionClientNames[framework] = displayName
}

// ExtensionClientName returns the display name of a registered extension
// client; ok is false for every other client.
func ExtensionClientName(framework string) (string, bool) {
	name, ok := extensionClientNames[framework]
	return name, ok
}
