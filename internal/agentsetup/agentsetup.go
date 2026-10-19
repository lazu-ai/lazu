// Package agentsetup renders the one-line installer scripts behind
// `curl -fsSL <origin>/setup/<tool> | sh -s -- --key …`. Each script is a
// shared prelude (argument parsing, JSON merge, test request) followed by the
// body that knows one agent's config file. The Key never reaches this server:
// it is passed to the script on the user's machine.
package agentsetup

import (
	"embed"
	"fmt"
	"net/url"
	"strings"
)

//go:embed scripts/*.sh scripts/*.ps1
var scripts embed.FS

// Shell is the script dialect a request asks for.
type Shell string

const (
	ShellPOSIX      Shell = "sh"
	ShellPowerShell Shell = "ps1"
)

// Tools lists every agent with a setup script, in the order the console shows
// them.
var Tools = []string{"claude-code", "codex", "gemini-cli", "opencode", "openclaw"}

func isTool(name string) bool {
	for _, tool := range Tools {
		if tool == name {
			return true
		}
	}
	return false
}

// ParseName splits a request path segment such as "codex" or "codex.ps1" into
// the tool and the dialect. ok is false for anything that is not a known tool.
func ParseName(segment string) (tool string, shell Shell, ok bool) {
	tool, shell = segment, ShellPOSIX
	switch {
	case strings.HasSuffix(segment, ".ps1"):
		tool, shell = strings.TrimSuffix(segment, ".ps1"), ShellPowerShell
	case strings.HasSuffix(segment, ".sh"):
		tool = strings.TrimSuffix(segment, ".sh")
	}
	return tool, shell, isTool(tool)
}

// NormalizeBaseURL returns an http(s) origin (optionally with a path) that is
// safe to paste into a single-quoted shell string, without a trailing slash.
func NormalizeBaseURL(raw string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	parsed, err := url.Parse(trimmed)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("base url must be an absolute http(s) URL")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return "", fmt.Errorf("base url must not carry a query, fragment or credentials")
	}
	// Brackets are here for IPv6 literals such as https://[2001:db8::1]:8443;
	// everything outside this set could change the meaning of the single-quoted
	// shell string the scripts carry.
	for _, r := range trimmed {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(".:/_-[]", r)
		if !ok {
			return "", fmt.Errorf("base url contains a character the scripts do not accept: %q", r)
		}
	}
	return trimmed, nil
}

// Render returns the full script for a tool. baseURL is baked in as the
// default the user can still override with --base-url / -BaseUrl.
func Render(tool string, shell Shell, baseURL string) (string, error) {
	if !isTool(tool) {
		return "", fmt.Errorf("unknown tool %q", tool)
	}
	base, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	ext := string(shell)
	if shell != ShellPOSIX && shell != ShellPowerShell {
		return "", fmt.Errorf("unknown shell %q", shell)
	}
	prelude, err := scripts.ReadFile("scripts/prelude." + ext)
	if err != nil {
		return "", err
	}
	body, err := scripts.ReadFile("scripts/" + tool + "." + ext)
	if err != nil {
		return "", err
	}
	replacer := strings.NewReplacer(
		"__BASE_URL__", base,
		"__ORIGIN__", base,
		"__TOOL__", tool,
	)
	return replacer.Replace(string(prelude) + string(body)), nil
}
