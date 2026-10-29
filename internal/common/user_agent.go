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

// ClientHeaders are the request headers that name a client besides the
// User-Agent. Agents built on a vendor SDK often leave the SDK's User-Agent
// in place and name themselves elsewhere: Codex and Roo Code send
// `originator`, Claude Code sends `x-app: cli`, and desktop apps such as
// Cherry Studio send the OpenRouter-style `X-Title`.
type ClientHeaders struct {
	UserAgent  string
	Originator string
	XApp       string
	XTitle     string
}

// ParseClientRequest is ParseClient plus the headers agents use to name
// themselves when the User-Agent belongs to an SDK or a runtime.
func ParseClientRequest(h ClientHeaders) ClientInfo {
	info := ParseClient(h.UserAgent)
	originator := strings.ToLower(strings.TrimSpace(h.Originator))
	xApp := strings.ToLower(strings.TrimSpace(h.XApp))

	// Codex names itself in `originator` on every request; anything else
	// that sets `originator` (Roo Code, pi, OpenClaw, Hermes on a Codex
	// host) is also telling us who it is.
	if originator != "" && !IsNamedClient(info.Framework) {
		if framework := originatorFramework(originator); framework != "" {
			info.Framework = framework
			info.Language = clientLanguage(framework)
		}
	}
	// Claude Code sends `x-app: cli` (`cli-bg` for background work); its own
	// User-Agent normally wins above, so this only catches a stripped one.
	if info.Framework == "" && (xApp == "cli" || xApp == "cli-bg") {
		info.Framework, info.Language = "claude-code", "node"
	}
	// Qwen Code impersonates Claude Code on every host but Anthropic's own,
	// with its own version number: real Claude Code has been 1.x and 2.x since
	// it shipped, Qwen Code is 0.x.
	if info.Framework == "claude-code" && xApp == "cli" && strings.HasPrefix(info.Version, "0.") {
		info.Framework = "qwen-code"
	}
	// X-Title names the app in OpenRouter's convention; a few desktop apps
	// send it to every provider.
	if !IsNamedClient(info.Framework) {
		if framework := titleFramework(h.XTitle); framework != "" {
			info.Framework, info.Language = framework, clientLanguage(framework)
		}
	}
	return info
}

// headerClients are the names agents and apps put in `originator` or
// `X-Title` when their User-Agent is a library's, as their source sends them.
// Only these are accepted: a slug of an arbitrary header would let one
// caller mint a new client tag per request and spread digests, sessions and
// the overview across them.
var headerClients = map[string]string{
	// originator
	"roo-code":     "roo-code",
	"kilo-code":    "kilo-code",
	"cline":        "cline",
	"pi":           "pi",
	"openclaw":     "openclaw",
	"hermes":       "hermes-agent",
	"hermes-agent": "hermes-agent",
	// X-Title
	"cherry-studio": "cherry-studio",
	"chatbox":       "chatbox",
	"chatbox-ai":    "chatbox",
	"open-webui":    "open-webui",
	"sillytavern":   "sillytavern",
	"librechat":     "librechat",
	"anythingllm":   "anythingllm",
	"aider":         "aider",
	"goose":         "goose",
	"crush":         "crush",
	"qwen-code":     "qwen-code",
	"void":          "void",
}

// originatorFramework maps an `originator` header to a client tag. Codex
// variants (codex_cli_rs, codex-tui, codex_exec, codex_vscode, "Codex
// Desktop") all collapse to codex; other agents must be in headerClients.
func originatorFramework(originator string) string {
	if strings.HasPrefix(originator, "codex") {
		return "codex"
	}
	return headerClients[clientSlug(originator)]
}

// titleFramework turns an `X-Title` such as "Cherry Studio" into
// "cherry-studio" when the app is one headerClients knows.
func titleFramework(title string) string {
	return headerClients[clientSlug(title)]
}

var slugStrip = regexp.MustCompile(`[^a-z0-9]+`)

// clientSlug lower-cases a client name and keeps only [a-z0-9-], so "Cherry
// Studio", "cherry_studio" and "CherryStudio " all look up the same entry.
func clientSlug(name string) string {
	return strings.Trim(slugStrip.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-"), "-")
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
	// library marks an SDK, HTTP client or browser: a transport that many
	// different programs share, so it is not named in digests or badges.
	library bool
}

// Patterns are tried in declaration order. Agents and apps that put their
// own name in the User-Agent come first, then the SDKs and frameworks they
// are built on, then generic HTTP clients and browsers.
//
// Every agent string below was read from that client's source on
// 2026-10-29 (the test file carries the examples). Agents built on a vendor
// SDK that do not name themselves (Goose sends no User-Agent at all; Cline
// v4, Continue, Kimi CLI, OpenClaw and Aider send the SDK's) can only be
// recognised by their headers, in ParseClientRequest and the session
// middleware.
var clientPatterns = []clientPattern{
	// Claude Code: `claude-cli/<ver> (external, cli|sdk-cli|sdk-ts|sdk-py)`.
	// The `claude-code/` form is its sign-in client and Hermes' impersonation
	// of it; proxies such as sub2api and CC Switch copy the real string.
	{re: regexp.MustCompile(`(?i)^claude-cli/([\d.]+)`), framework: "claude-code", language: "node"},
	{re: regexp.MustCompile(`(?i)\bclaude[ -]?code/([\d.]+)`), framework: "claude-code", language: "node"},
	// Hermes names itself only on special routes, including a Codex-shaped
	// `codex_cli_rs/0.0.0 (Hermes Agent)`, so it goes before Codex.
	{re: regexp.MustCompile(`(?i)\bhermes[ -]?agent(?:/([\d.]+))?`), framework: "hermes-agent", language: "python"},
	// Codex: `<originator>/<ver> (<os>; <arch>) <terminal> (<client>; <ver>)`
	// with originator codex_cli_rs, codex-tui, codex_exec, codex_vscode,
	// or `Codex Desktop`.
	{re: regexp.MustCompile(`(?i)^codex(?:[_-][a-z_-]+)?/([\d.]+)`), framework: "codex", language: "rust"},
	{re: regexp.MustCompile(`(?i)^codex\b`), framework: "codex", language: "rust"},
	// OpenCode: `opencode/<channel>/<ver>/<client>` (channel is the npm
	// dist-tag, e.g. latest); older builds sent `opencode/<ver>`.
	{re: regexp.MustCompile(`(?i)^opencode/(?:[\w.-]+/)*?([\d.]+)`), framework: "opencode", language: "node"},
	// Kilo Code: `Kilo-Code/<ver>` on most providers; its Anthropic provider
	// keeps the opencode-style `kilo/<channel>/<ver>/cli`.
	{re: regexp.MustCompile(`(?i)^kilo-?code/([\d.]+)`), framework: "kilo-code", language: "node"},
	{re: regexp.MustCompile(`(?i)^kilo/(?:[\w.-]+/)*?([\d.]+)`), framework: "kilo-code", language: "node"},
	// Roo Code: `RooCode/<ver>` (OpenAI-compatible provider) or
	// `roo-code/<ver> (<os>; <arch>) node/<ver>` (native OpenAI provider).
	{re: regexp.MustCompile(`(?i)^roo-?code/([\d.]+)`), framework: "roo-code", language: "node"},
	{re: regexp.MustCompile(`(?i)Charm-Crush/([\w.]+)`), framework: "crush", language: "go"},
	// OpenClaw names itself only to vendors' own hosts: `openclaw/<ver>`, or
	// `openclaw (<os>; <arch>)` on the ChatGPT path.
	{re: regexp.MustCompile(`(?i)^openclaw(?:/([\d.]+))?(?:\s|$)`), framework: "openclaw", language: "node"},
	// pi: `pi/1.4.0 (darwin; node/v22.12.0; arm64)`; early builds sent
	// `pi (darwin 25.2.0; arm64)` with no version.
	{re: regexp.MustCompile(`(?i)^pi(?:/([\d.]+))? \(`), framework: "pi", language: "node"},
	// Gemini CLI: `GeminiCLI-tui/0.66.0/gemini-2.5-pro (darwin; arm64; terminal)`,
	// `CloudCodeVSCode/<ver> (…)` from VS Code, `gemini-cli/<ver>` before 0.4.
	{re: regexp.MustCompile(`(?i)^GeminiCLI(?:-[\w-]+)?/([\d.]+)`), framework: "gemini-cli", language: "node"},
	{re: regexp.MustCompile(`(?i)^CloudCodeVSCode/([\d.]+)`), framework: "gemini-cli", language: "node"},
	{re: regexp.MustCompile(`(?i)^gemini[- ]cli/([\d.]+)`), framework: "gemini-cli", language: "node"},
	{re: regexp.MustCompile(`(?i)^QwenCode/([\d.]+)`), framework: "qwen-code", language: "node"},
	{re: regexp.MustCompile(`(?i)^KimiCLI/([\d.]+)`), framework: "kimi-cli", language: "python"},
	{re: regexp.MustCompile(`(?i)^iFlow-?Cli(?:/([\d.]+))?`), framework: "iflow-cli", language: "node"},
	{re: regexp.MustCompile(`(?i)^GitHubCopilotChat/([\d.]+)`), framework: "github-copilot", language: "node"},
	{re: regexp.MustCompile(`(?i)^Jan-Agent/([\d.]+)`), framework: "jan", language: "rust"},
	{re: regexp.MustCompile(`(?i)^Cline/([\d.]+)`), framework: "cline", language: "node"},
	{re: regexp.MustCompile(`(?i)^Zed/([\d.]+)`), framework: "zed", language: "rust"},
	{re: regexp.MustCompile(`(?i)^Cursor/([\d.]+)`), framework: "cursor", language: "node"},
	{re: regexp.MustCompile(`(?i)^Continue(?:\.dev)?/(?:([\d.]+)|IDE)`), framework: "continue", language: "node"},
	{re: regexp.MustCompile(`(?i)^Aider/([\d.]+)`), framework: "aider", language: "python"},
	{re: regexp.MustCompile(`(?i)^goose(?:[-_][\w-]+)?(?:/([\d.]+))?(?:\s|$)`), framework: "goose", language: "rust"},
	{re: regexp.MustCompile(`(?i)^cc-switch/([\d.]+)`), framework: "cc-switch", language: "rust"},

	// Desktop and web apps that name themselves.
	{re: regexp.MustCompile(`(?i)^AnythingLLM/([\w.]+)`), framework: "anythingllm", language: "node"},
	{re: regexp.MustCompile(`(?i)^lobehub/([\d.]+)`), framework: "lobechat", language: "node"},
	// Electron apps cannot change the renderer's User-Agent; their name is
	// the app token Electron adds: `… CherryStudio/1.9.13 Chrome/… Electron/…`.
	{re: regexp.MustCompile(`(?i)\bCherryStudio/([\d.]+)`), framework: "cherry-studio", language: "browser"},
	{re: regexp.MustCompile(`(?i)\b(?:xyz\.chatboxapp\.\w+|chatbox)/([\d.]+)`), framework: "chatbox", language: "browser"},
	{re: regexp.MustCompile(`(?i)^Agents/(?:Python|JavaScript)\s+([\d.]+)`), framework: "openai-agents", language: "python"},
	{re: regexp.MustCompile(`(?i)^mastra(?:/([\d.]+))?(?:\s|$)`), framework: "mastra", language: "node"},

	// LLM tooling layers go before the SDKs they wrap, since several prepend
	// their own token to the SDK's string (semantic-kernel-python/… AsyncOpenAI/Python …).
	{re: regexp.MustCompile(`(?i)\blangchain(?:js)?(?:-[a-z-]+)?/([\d.]+)`), framework: "langchain", language: "python", library: true},
	{re: regexp.MustCompile(`(?i)\bLangChain\b`), framework: "langchain", language: "python", library: true},
	{re: regexp.MustCompile(`(?i)\bLiteLLM/([\d.]+)`), framework: "litellm", language: "python", library: true},
	{re: regexp.MustCompile(`(?i)\bllama[-_ ]?index(?:/([\d.]+))?`), framework: "llama-index", language: "python", library: true},
	{re: regexp.MustCompile(`(?i)^pydantic-ai/([\d.]+)`), framework: "pydantic-ai", language: "python", library: true},
	{re: regexp.MustCompile(`(?i)\bsemantic-kernel(?:-python)?/([\d.]+)`), framework: "semantic-kernel", language: "python", library: true},
	{re: regexp.MustCompile(`(?i)^Semantic-Kernel\b`), framework: "semantic-kernel", language: "dotnet", library: true},
	{re: regexp.MustCompile(`(?i)^spring-ai\b`), framework: "spring-ai", language: "java", library: true},
	// Vercel AI SDK: `ai-sdk/openai-compatible/2.0.72 ai-sdk/provider-utils/… runtime/node.js/22`
	// (v5/v6) or `ai-sdk-openai/4.0.104 ai-sdk-provider-utils/… node.js/22` (v7).
	{re: regexp.MustCompile(`(?i)(?:^|\s)ai-sdk[/-][\w-]+/([\d.]+)`), framework: "ai-sdk", language: "node", library: true},

	// Official SDKs. The class name is part of the string, so async and
	// Azure clients read AsyncOpenAI/Python, AzureOpenAI/Python.
	{re: regexp.MustCompile(`(?i)\b(?:Async|Azure)?OpenAI/Python\s+([\d.]+)`), framework: "openai-python", language: "python", library: true},
	{re: regexp.MustCompile(`(?i)\bOpenAI[ /]NodeJS\s+([\d.]+)`), framework: "openai-node", language: "node", library: true},
	{re: regexp.MustCompile(`(?i)\bOpenAI(?:CompatibleClient)?/JS\s+([\d.]+)`), framework: "openai-node", language: "node", library: true},
	{re: regexp.MustCompile(`(?i)\bOpenAI/Go\s+([\d.]+)`), framework: "openai-go", language: "go", library: true},
	{re: regexp.MustCompile(`(?i)\bOpenAIClient(?:Async)?Impl/Java\s+([\d.]+)`), framework: "openai-java", language: "java", library: true},
	{re: regexp.MustCompile(`(?i)\bOpenAI/([\d.]+) \(\.NET`), framework: "openai-dotnet", language: "dotnet", library: true},
	{re: regexp.MustCompile(`(?i)\b(?:Async)?Anthropic/JS\s+([\d.]+)`), framework: "anthropic-typescript", language: "node", library: true},
	{re: regexp.MustCompile(`(?i)\b(?:Async)?Anthropic/Python\s+([\d.]+)`), framework: "anthropic-python", language: "python", library: true},
	{re: regexp.MustCompile(`(?i)\bAnthropic/Go\s+([\d.]+)`), framework: "anthropic-go", language: "go", library: true},
	{re: regexp.MustCompile(`(?i)\bAnthropicClient(?:Async)?Impl/Java\s+([\d.]+)`), framework: "anthropic-java", language: "java", library: true},
	{re: regexp.MustCompile(`(?i)anthropic-sdk-python[ /]?([\d.]+)?`), framework: "anthropic-python", language: "python", library: true},
	{re: regexp.MustCompile(`(?i)anthropic-sdk-typescript[ /]?([\d.]+)?`), framework: "anthropic-typescript", language: "node", library: true},
	{re: regexp.MustCompile(`(?i)anthropic-ai/sdk[ /]?([\d.]+)?`), framework: "anthropic-typescript", language: "node", library: true},
	// Bundled apps minify the SDK's class name: `Fn/JS 6.9.0`, `XE/JS 0.51.0`.
	// OpenAI and Anthropic cannot be told apart from the string alone.
	{re: regexp.MustCompile(`^[\w$]{1,4}/JS\s+([\d.]+)$`), framework: "stainless-js", language: "node", library: true},
	{re: regexp.MustCompile(`(?i)\bgoogle-genai-sdk/([\d.]+)`), framework: "google-genai", language: "python", library: true},
	{re: regexp.MustCompile(`(?i)google-generativeai[ /]?([\d.]+)?`), framework: "google-genai-python", language: "python", library: true},
	{re: regexp.MustCompile(`(?i)mistralai[ /]?([\d.]+)?`), framework: "mistral-python", language: "python", library: true},

	// Generic HTTP / runtime.
	{re: regexp.MustCompile(`(?i)^curl/([\d.]+)`), framework: "curl", language: "shell", library: true},
	{re: regexp.MustCompile(`(?i)^Wget/([\d.]+)`), framework: "wget", language: "shell", library: true},
	{re: regexp.MustCompile(`(?i)^httpie[ /]?([\d.]+)?`), framework: "httpie", language: "shell", library: true},
	{re: regexp.MustCompile(`(?i)^Postman[\w ]*[ /]?([\d.]+)?`), framework: "postman", language: "browser", library: true},
	{re: regexp.MustCompile(`(?i)^Insomnia[ /]?([\d.]+)?`), framework: "insomnia", language: "browser", library: true},
	{re: regexp.MustCompile(`(?i)^axios/([\d.]+)`), framework: "axios", language: "node", library: true},
	{re: regexp.MustCompile(`(?i)^node-fetch(?:/([\d.]+))?`), framework: "node-fetch", language: "node", library: true},
	{re: regexp.MustCompile(`(?i)^(?:node|undici)(?:/([\d.]+))?$`), framework: "node-fetch", language: "node", library: true},
	{re: regexp.MustCompile(`(?i)^Bun/([\d.]+)`), framework: "bun", language: "node", library: true},
	{re: regexp.MustCompile(`(?i)^python-requests/([\d.]+)`), framework: "python-requests", language: "python", library: true},
	{re: regexp.MustCompile(`(?i)^python-httpx/([\d.]+)`), framework: "python-httpx", language: "python", library: true},
	{re: regexp.MustCompile(`(?i)^Python-urllib/([\d.]+)`), framework: "python-urllib", language: "python", library: true},
	{re: regexp.MustCompile(`(?i)\bokhttp/([\d.]+)`), framework: "okhttp", language: "java", library: true},
	{re: regexp.MustCompile(`(?i)\baiohttp/([\d.]+)`), framework: "aiohttp", language: "python", library: true},
	{re: regexp.MustCompile(`(?i)^Go-http-client/([\d.]+)`), framework: "go-http", language: "go", library: true},
	{re: regexp.MustCompile(`(?i)^reqwest/([\d.]+)`), framework: "reqwest", language: "rust", library: true},
	{re: regexp.MustCompile(`(?i)^(?:Java-http-client|Apache-HttpClient|Java)/([\d._]+)`), framework: "java-http", language: "java", library: true},
	{re: regexp.MustCompile(`(?i)^ktor-client(?:/([\d.]+))?`), framework: "ktor", language: "java", library: true},

	// Browsers — last resort, distinguishes "user pasted curl into a
	// browser console" from "real desktop browser hitting the API". An
	// Electron app whose token we do not know is at least not a browser.
	{re: regexp.MustCompile(`(?i)\bElectron/([\d.]+)`), framework: "electron", language: "browser", library: true},
	{contains: "chrome/", framework: "browser-chrome", language: "browser", library: true},
	{contains: "firefox/", framework: "browser-firefox", language: "browser", library: true},
	{contains: "safari/", framework: "browser-safari", language: "browser", library: true},
}

var (
	extensionClientNames = map[string]string{}
	libraryClients       = map[string]bool{}
	namedClients         = map[string]string{}
)

func init() {
	for _, p := range clientPatterns {
		if p.library {
			libraryClients[p.framework] = true
		} else {
			namedClients[p.framework] = p.language
		}
	}
	for _, framework := range headerClients {
		if _, ok := namedClients[framework]; !ok {
			namedClients[framework] = ""
		}
	}
}

// RegisterClientPattern recognises an extension's own client by its
// User-Agent ahead of every built-in pattern, and names it in digests.
func RegisterClientPattern(pattern string, framework string, language string, displayName string) {
	clientPatterns = append([]clientPattern{{re: regexp.MustCompile(pattern), framework: framework, language: language}}, clientPatterns...)
	extensionClientNames[framework] = displayName
	namedClients[framework] = language
}

// ExtensionClientName returns the display name of a registered extension
// client; ok is false for every other client.
func ExtensionClientName(framework string) (string, bool) {
	name, ok := extensionClientNames[framework]
	return name, ok
}

// IsLibraryClient reports whether a client tag names a transport many
// programs share (an SDK, an HTTP client, a browser) rather than a program.
func IsLibraryClient(framework string) bool {
	return libraryClients[strings.ToLower(strings.TrimSpace(framework))]
}

// IsNamedClient reports whether a client tag names a program: an agent or
// app from the User-Agent table or the header allowlist, or a registered
// extension client. Digests and the console group everything else as
// direct API.
func IsNamedClient(framework string) bool {
	_, ok := namedClients[strings.ToLower(strings.TrimSpace(framework))]
	return ok
}

// IsAgentClient is IsNamedClient under the name the call sites read best.
func IsAgentClient(framework string) bool {
	return IsNamedClient(framework)
}

func clientLanguage(framework string) string {
	return namedClients[framework]
}

// clientDisplayNames spells the recognised clients the way they spell
// themselves. Anything else is shown as its tag.
var clientDisplayNames = map[string]string{
	"claude-code":          "Claude Code",
	"codex":                "Codex",
	"opencode":             "OpenCode",
	"kilo-code":            "Kilo Code",
	"roo-code":             "Roo Code",
	"cline":                "Cline",
	"crush":                "Crush",
	"goose":                "Goose",
	"pi":                   "pi",
	"openclaw":             "OpenClaw",
	"gemini-cli":           "Gemini CLI",
	"qwen-code":            "Qwen Code",
	"kimi-cli":             "Kimi CLI",
	"iflow-cli":            "iFlow CLI",
	"github-copilot":       "GitHub Copilot",
	"jan":                  "Jan",
	"hermes-agent":         "Hermes Agent",
	"cursor":               "Cursor",
	"zed":                  "Zed",
	"aider":                "Aider",
	"continue":             "Continue",
	"cc-switch":            "CC Switch",
	"anythingllm":          "AnythingLLM",
	"lobechat":             "LobeChat",
	"cherry-studio":        "Cherry Studio",
	"chatbox":              "Chatbox",
	"open-webui":           "Open WebUI",
	"sillytavern":          "SillyTavern",
	"librechat":            "LibreChat",
	"void":                 "Void",
	"openai-agents":        "OpenAI Agents SDK",
	"mastra":               "Mastra",
	"openai-python":        "OpenAI SDK (Python)",
	"openai-node":          "OpenAI SDK (Node)",
	"openai-go":            "OpenAI SDK (Go)",
	"openai-java":          "OpenAI SDK (Java)",
	"openai-dotnet":        "OpenAI SDK (.NET)",
	"anthropic-python":     "Anthropic SDK (Python)",
	"anthropic-typescript": "Anthropic SDK (TypeScript)",
	"anthropic-go":         "Anthropic SDK (Go)",
	"anthropic-java":       "Anthropic SDK (Java)",
	"stainless-js":         "OpenAI / Anthropic SDK (bundled)",
	"google-genai":         "Google GenAI SDK",
	"google-genai-python":  "Google GenAI SDK",
	"mistral-python":       "Mistral SDK",
	"langchain":            "LangChain",
	"litellm":              "LiteLLM",
	"llama-index":          "LlamaIndex",
	"pydantic-ai":          "PydanticAI",
	"semantic-kernel":      "Semantic Kernel",
	"spring-ai":            "Spring AI",
	"ai-sdk":               "Vercel AI SDK",
	"electron":             "Electron app",
	"browser-chrome":       "Chrome",
	"browser-firefox":      "Firefox",
	"browser-safari":       "Safari",
}

// ClientDisplayName returns the human name of a client tag: the extension's
// registered name, the table's spelling, or the tag itself.
func ClientDisplayName(framework string) string {
	key := strings.ToLower(strings.TrimSpace(framework))
	if name, ok := extensionClientNames[key]; ok {
		return name
	}
	if name, ok := clientDisplayNames[key]; ok {
		return name
	}
	return framework
}
