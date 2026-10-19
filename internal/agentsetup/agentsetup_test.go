package agentsetup

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseName(t *testing.T) {
	cases := []struct {
		in    string
		tool  string
		shell Shell
		ok    bool
	}{
		{"claude-code", "claude-code", ShellPOSIX, true},
		{"codex.sh", "codex", ShellPOSIX, true},
		{"gemini-cli.ps1", "gemini-cli", ShellPowerShell, true},
		{"cursor", "cursor", ShellPOSIX, false},
		{"../prelude", "../prelude", ShellPOSIX, false},
		{"prelude.sh", "prelude", ShellPOSIX, false},
	}
	for _, tc := range cases {
		tool, shell, ok := ParseName(tc.in)
		if tool != tc.tool || shell != tc.shell || ok != tc.ok {
			t.Errorf("ParseName(%q) = %q, %q, %v; want %q, %q, %v", tc.in, tool, shell, ok, tc.tool, tc.shell, tc.ok)
		}
	}
}

func TestNormalizeBaseURLRejectsShellMetacharacters(t *testing.T) {
	good := map[string]string{
		"https://api.lazu.ai/":        "https://api.lazu.ai",
		" http://localhost:3000 ":     "http://localhost:3000",
		"https://gw.example.com/lazu": "https://gw.example.com/lazu",
	}
	for in, want := range good {
		got, err := NormalizeBaseURL(in)
		if err != nil || got != want {
			t.Errorf("NormalizeBaseURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "api.lazu.ai", "ftp://x", "https://x/'$(id)'", "https://x/?a=1", "https://u:p@x", "https://x/a b"} {
		if _, err := NormalizeBaseURL(bad); err == nil {
			t.Errorf("NormalizeBaseURL(%q) accepted a value it must refuse", bad)
		}
	}
}

func TestRenderEveryToolAndShell(t *testing.T) {
	for _, tool := range Tools {
		for _, shell := range []Shell{ShellPOSIX, ShellPowerShell} {
			script, err := Render(tool, shell, "https://api.lazu.ai/")
			if err != nil {
				t.Fatalf("Render(%s, %s): %v", tool, shell, err)
			}
			if strings.Contains(script, "__") && strings.Contains(script, "__BASE_URL__") {
				t.Fatalf("Render(%s, %s) left a placeholder", tool, shell)
			}
			for _, placeholder := range []string{"__BASE_URL__", "__ORIGIN__", "__TOOL__"} {
				if strings.Contains(script, placeholder) {
					t.Fatalf("Render(%s, %s) left %s", tool, shell, placeholder)
				}
			}
			if !strings.Contains(script, "https://api.lazu.ai") {
				t.Fatalf("Render(%s, %s) does not carry the base URL", tool, shell)
			}
		}
	}
	if _, err := Render("cursor", ShellPOSIX, "https://api.lazu.ai"); err == nil {
		t.Fatal("Render accepted a tool without a script")
	}
}

func TestPOSIXScriptsParse(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	for _, tool := range Tools {
		script, err := Render(tool, ShellPOSIX, "https://api.lazu.ai")
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(sh, "-n")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: sh -n failed: %v\n%s", tool, err, out)
		}
	}
}

// TestPOSIXScriptsWriteConfig runs every script against a throwaway HOME with
// the test request skipped, and checks the file each agent reads.
func TestPOSIXScriptsWriteConfig(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	_, nodeErr := exec.LookPath("node")
	_, pyErr := exec.LookPath("python3")
	if nodeErr != nil && pyErr != nil {
		t.Skip("neither node nor python3 available")
	}
	const key = "sk-lazu-test0123456789"
	cases := []struct {
		tool  string
		args  []string
		file  string
		wants []string
	}{
		{"claude-code", []string{"--model", "claude-sonnet-5", "--opus", "claude-opus-5", "--sonnet", "claude-sonnet-5", "--haiku", "deepseek-v4-flash"}, ".claude/settings.json",
			[]string{`"ANTHROPIC_BASE_URL": "https://api.lazu.ai"`, `"ANTHROPIC_AUTH_TOKEN": "` + key + `"`, `"ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus-5"`, `"ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-sonnet-5"`, `"ANTHROPIC_DEFAULT_HAIKU_MODEL": "deepseek-v4-flash"`, `"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": "1"`, `"theme": "dark"`}},
		{"codex", []string{"--model", "gpt-5.2", "--also", "kimi-k3", "--also", "gpt-5.2", "--also", "claude-sonnet-5"}, ".codex/config.toml",
			[]string{`model_provider = "lazu"`, `model = "gpt-5.2"`, `model_catalog_json = "`, `/.codex/lazu-models.json"`, `base_url = "https://api.lazu.ai/v1"`, `env_key = "LAZU_API_KEY"`, `wire_api = "responses"`, `[profiles.keep]`}},
		{"gemini-cli", []string{"--model", "gemini-2.5-pro"}, ".gemini/.env",
			[]string{"GEMINI_API_KEY=" + key, "GOOGLE_GEMINI_BASE_URL=https://api.lazu.ai", "GEMINI_MODEL=gemini-2.5-pro"}},
		// --also adds to the provider's list; a repeat of the default or light
		// model, or of another --also, is written once.
		{"opencode", []string{"--model", "claude-sonnet-5", "--small", "deepseek-v4-flash", "--also", "claude-opus-5", "--also", "deepseek-v4-flash", "--also", "gpt-5.2", "--also", "gpt-5.2"}, ".config/opencode/opencode.json",
			[]string{`"baseURL": "https://api.lazu.ai/v1"`, `"model": "lazu/claude-sonnet-5"`, `"small_model": "lazu/deepseek-v4-flash"`, `"claude-opus-5": {`, `"gpt-5.2": {`}},
		{"openclaw", []string{"--model", "claude-sonnet-5", "--also", "claude-opus-5", "--also", "claude-sonnet-5"}, ".openclaw/openclaw.json",
			[]string{`"api": "openai-completions"`, `"model": "lazu/claude-sonnet-5"`, `"id": "claude-opus-5"`}},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			home := t.TempDir()
			// Pre-existing config the script must keep.
			switch tc.tool {
			case "claude-code":
				mustWrite(t, filepath.Join(home, ".claude/settings.json"), `{"theme": "dark", "env": {"ANTHROPIC_API_KEY": "old"}}`)
			case "codex":
				mustWrite(t, filepath.Join(home, ".codex/config.toml"), "model = \"o3\"\n\n[profiles.keep]\nmodel = \"o3\"\n\n[model_providers.lazu]\nname = \"stale\"\n")
			}
			script, err := Render(tc.tool, ShellPOSIX, "https://api.lazu.ai")
			if err != nil {
				t.Fatal(err)
			}
			args := append([]string{"-s", "--", "--key", key, "--no-check"}, tc.args...)
			cmd := exec.Command(sh, args...)
			cmd.Stdin = strings.NewReader(script)
			cmd.Env = []string{"HOME=" + home, "PATH=" + fakeCodexPath(t) + ":" + os.Getenv("PATH"), "SHELL=/bin/zsh"}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("script failed: %v\n%s", err, out)
			}
			data, err := os.ReadFile(filepath.Join(home, tc.file))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.wants {
				if !strings.Contains(string(data), want) {
					t.Errorf("%s missing %q:\n%s", tc.file, want, data)
				}
			}
			switch tc.tool {
			case "opencode":
				if n := strings.Count(string(data), `"gpt-5.2": {`); n != 1 {
					t.Errorf("gpt-5.2 written %d times:\n%s", n, data)
				}
				if n := strings.Count(string(data), `"deepseek-v4-flash": {`); n != 1 {
					t.Errorf("light model written %d times into the list:\n%s", n, data)
				}
			case "openclaw":
				if n := strings.Count(string(data), `"id": "claude-sonnet-5"`); n != 1 {
					t.Errorf("default model written %d times into the list:\n%s", n, data)
				}
			case "claude-code":
				if strings.Contains(string(data), "ANTHROPIC_API_KEY") {
					t.Errorf("stale ANTHROPIC_API_KEY kept:\n%s", data)
				}
			case "codex":
				text := string(data)
				topLevel := text[:strings.Index(text, "[profiles.keep]")]
				if strings.Count(text, "[model_providers.lazu]") != 1 || strings.Contains(text, `name = "stale"`) || strings.Contains(topLevel, `model = "o3"`) || !strings.Contains(text, "[profiles.keep]\nmodel = \"o3\"") {
					t.Errorf("codex config not rewritten cleanly:\n%s", text)
				}
				catalog, err := os.ReadFile(filepath.Join(home, ".codex/lazu-models.json"))
				if err != nil {
					t.Fatalf("catalog not written: %v", err)
				}
				var parsed struct {
					Models []struct {
						Slug             string   `json:"slug"`
						Priority         int      `json:"priority"`
						BaseInstructions string   `json:"base_instructions"`
						ToolMode         *string  `json:"tool_mode"`
						InputModalities  []string `json:"input_modalities"`
						Levels           []struct {
							Effort string `json:"effort"`
						} `json:"supported_reasoning_levels"`
					} `json:"models"`
				}
				if err := json.Unmarshal(catalog, &parsed); err != nil {
					t.Fatalf("catalog is not JSON: %v\n%s", err, catalog)
				}
				var slugs []string
				for _, m := range parsed.Models {
					slugs = append(slugs, m.Slug)
				}
				if strings.Join(slugs, ",") != "gpt-5.2,kimi-k3,claude-sonnet-5" {
					t.Errorf("catalog models = %v", slugs)
				}
				if len(parsed.Models[0].Levels) != 5 || len(parsed.Models[1].Levels) != 3 {
					t.Errorf("reasoning levels: gpt %d, kimi %d", len(parsed.Models[0].Levels), len(parsed.Models[1].Levels))
				}
				// Every entry is a clone of the installed Codex's own entry, so
				// the version-specific fields (the base prompt here) come along
				// and the OpenAI-only ones are dropped.
				for _, m := range parsed.Models {
					if m.BaseInstructions != "the bundled prompt" {
						t.Errorf("%s lost base_instructions: %q", m.Slug, m.BaseInstructions)
					}
					if m.ToolMode != nil {
						t.Errorf("%s kept tool_mode", m.Slug)
					}
				}
				if strings.Join(parsed.Models[1].InputModalities, ",") != "text" || strings.Join(parsed.Models[2].InputModalities, ",") != "text,image" {
					t.Errorf("input modalities: kimi %v, claude %v", parsed.Models[1].InputModalities, parsed.Models[2].InputModalities)
				}
				rc, err := os.ReadFile(filepath.Join(home, ".zshrc"))
				if err != nil || !strings.Contains(string(rc), "export LAZU_API_KEY='"+key+"' # lazu") {
					t.Errorf("LAZU_API_KEY not added to .zshrc: %v\n%s", err, rc)
				}
			case "gemini-cli":
				settings, err := os.ReadFile(filepath.Join(home, ".gemini/settings.json"))
				if err != nil || !strings.Contains(string(settings), `"selectedType": "gemini-api-key"`) {
					t.Errorf("gemini auth type not set: %v\n%s", err, settings)
				}
			}
		})
	}
}

func TestPOSIXScriptRefusesInjectedKey(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	script, err := Render("claude-code", ShellPOSIX, "https://api.lazu.ai")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	cmd := exec.Command(sh, "-s", "--", "--key", `sk-x"; touch pwned; "`, "--no-check")
	cmd.Stdin = strings.NewReader(script)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	if err := cmd.Run(); err == nil {
		t.Fatal("script accepted a Key with shell metacharacters")
	}
	if _, err := os.Stat(filepath.Join(home, "pwned")); err == nil {
		t.Fatal("injected command ran")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The console's installer line exports LAZU_API_KEY rather than passing the
// Key as an argument, where every account on the machine could read it out of
// the process list. The scripts have to accept it that way.
func TestPOSIXScriptTakesKeyFromEnvironment(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	script, err := Render("claude-code", ShellPOSIX, "https://api.lazu.ai")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	settings := filepath.Join(home, ".claude/settings.json")
	// An existing config the rest of the machine can read must come out private.
	mustWrite(t, settings, `{"env": {"KEEP": "yes"}}`)
	if err := os.Chmod(settings, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(sh, "-s", "--", "--no-check")
	cmd.Stdin = strings.NewReader(script)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "LAZU_API_KEY=sk-lazu-fake-from-env"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	written, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "sk-lazu-fake-from-env") {
		t.Fatalf("Key from the environment was not written:\n%s", written)
	}
	if !strings.Contains(string(written), `"KEEP": "yes"`) {
		t.Fatalf("existing settings were dropped:\n%s", written)
	}
	info, err := os.Stat(settings)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("config holding the Key is mode %#o, want 0600", mode)
	}
}

func TestPOSIXScriptRefusesWithoutAKey(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	script, err := Render("codex", ShellPOSIX, "https://api.lazu.ai")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	cmd := exec.Command(sh, "-s", "--", "--model", "gpt-6-astra", "--no-check")
	cmd.Stdin = strings.NewReader(script)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	if err := cmd.Run(); err == nil {
		t.Fatal("script ran without a Key")
	}
}

// Codex reads the Key from the environment, so every startup file it lands in
// ends up holding a secret. None of them — nor the backups beside them — may
// stay world-readable, and a login shell has to see the Key as well.
func TestCodexScriptKeepsTheKeyFileOwnerOnly(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	script, err := Render("codex", ShellPOSIX, "https://api.lazu.ai")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	rc := filepath.Join(home, ".bashrc")
	mustWrite(t, rc, "export EDITOR=vi\n")
	if err := os.Chmod(rc, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(sh, "-s", "--", "--model", "gpt-6-astra", "--no-check")
	cmd.Stdin = strings.NewReader(script)
	cmd.Dir = home
	cmd.Env = []string{
		"HOME=" + home,
		"PATH=" + os.Getenv("PATH"),
		"SHELL=/bin/bash",
		"LAZU_API_KEY=sk-lazu-fake-codex-probe",
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}

	written, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "sk-lazu-fake-codex-probe") {
		t.Fatalf("Key was not written into the rc file:\n%s", written)
	}
	if !strings.Contains(string(written), "export EDITOR=vi") {
		t.Fatalf("the user's own rc lines were dropped:\n%s", written)
	}

	// A login bash (ssh, a macOS terminal profile) never reads .bashrc, so the
	// Key has to reach the login profile too or Codex starts without it.
	profile, err := os.ReadFile(filepath.Join(home, ".profile"))
	if err != nil {
		t.Fatalf("no login profile was written: %v", err)
	}
	if !strings.Contains(string(profile), "sk-lazu-fake-codex-probe") {
		t.Errorf("login profile does not carry the Key:\n%s", profile)
	}

	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		isRC := name == ".bashrc" || strings.HasPrefix(name, ".bashrc.lazu-backup-")
		isProfile := name == ".profile" || strings.HasPrefix(name, ".profile.lazu-backup-")
		if !isRC && !isProfile {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode&0o077 != 0 {
			t.Errorf("%s is mode %#o; a file holding a Key must be owner-only", name, mode)
		}
		checked++
	}
	if checked < 3 {
		t.Fatalf("expected .bashrc, its backup and the login profile, checked %d", checked)
	}
	if _, err := os.Stat(rc + ".lazu-tmp"); err == nil {
		t.Error("the scratch copy holding the Key was left behind")
	}
}

// A deployment may enable any model name its channels carry. The console
// quotes it for the shell; the script has to write it into JSON, TOML, a
// dotenv line and a URL without corrupting any of them — and without
// refusing it, which is what an allowlist here used to do.
func TestScriptsCarryAnAwkwardModelID(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	const model = `weird "model" \with/ spaces 'and' #hash`

	run := func(t *testing.T, tool string, args ...string) string {
		t.Helper()
		script, err := Render(tool, ShellPOSIX, "https://api.lazu.ai")
		if err != nil {
			t.Fatal(err)
		}
		home := t.TempDir()
		cmd := exec.Command(sh, append([]string{"-s", "--"}, args...)...)
		cmd.Stdin = strings.NewReader(script)
		cmd.Dir = home
		cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "LAZU_API_KEY=sk-lazu-fake-probe"}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s refused the model id: %v\n%s", tool, err, out)
		}
		return home
	}

	t.Run("claude-code writes valid JSON", func(t *testing.T) {
		home := run(t, "claude-code", "--model", model, "--no-check")
		raw, err := os.ReadFile(filepath.Join(home, ".claude/settings.json"))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Env map[string]string `json:"env"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("settings are not valid JSON: %v\n%s", err, raw)
		}
		if doc.Env["ANTHROPIC_MODEL"] != model {
			t.Errorf("model = %q, want %q", doc.Env["ANTHROPIC_MODEL"], model)
		}
	})

	t.Run("codex writes a readable TOML string", func(t *testing.T) {
		home := run(t, "codex", "--model", model, "--no-check")
		raw, err := os.ReadFile(filepath.Join(home, ".codex/config.toml"))
		if err != nil {
			t.Fatal(err)
		}
		line := ""
		for _, candidate := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(candidate, "model = ") {
				line = strings.TrimPrefix(candidate, "model = ")
				break
			}
		}
		// A TOML basic string escapes exactly as a JSON string does.
		var got string
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("model line is not a quoted string: %v\n%s", err, raw)
		}
		if got != model {
			t.Errorf("model = %q, want %q", got, model)
		}
	})

	t.Run("gemini writes one dotenv assignment", func(t *testing.T) {
		home := run(t, "gemini-cli", "--model", model, "--no-check")
		raw, err := os.ReadFile(filepath.Join(home, ".gemini/.env"))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		if len(lines) != 3 {
			t.Fatalf("expected three assignments, got %d:\n%s", len(lines), raw)
		}
		value := strings.TrimPrefix(lines[2], "GEMINI_MODEL=")
		var got string
		if err := json.Unmarshal([]byte(value), &got); err != nil {
			t.Fatalf("model value is not quoted: %v\n%s", err, raw)
		}
		if got != model {
			t.Errorf("model = %q, want %q", got, model)
		}
	})
}

// The verification request carries the Key in a header. Headers reach curl
// through its config on stdin, never as arguments, where `ps` would show them
// to every account on the machine — and they still have to arrive.
func TestCheckRequestSendsHeadersWithoutArgv(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	var seen http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	script, err := Render("claude-code", ShellPOSIX, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	cmd := exec.Command(sh, "-s", "--", "--model", "claude-fable-5-1")
	cmd.Stdin = strings.NewReader(script)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "LAZU_API_KEY=sk-lazu-fake-header-probe"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Test request OK") {
		t.Fatalf("the verification request did not run:\n%s", out)
	}
	if got := seen.Get("x-api-key"); got != "sk-lazu-fake-header-probe" {
		t.Errorf("x-api-key = %q, want the Key", got)
	}
	if got := seen.Get("anthropic-version"); got != "2023-06-01" {
		t.Errorf("anthropic-version = %q", got)
	}
	if got := seen.Get("content-type"); got != "application/json" {
		t.Errorf("content-type = %q", got)
	}
}

// A config that parses but is not an object cannot hold the keys we merge in:
// the write would silently keep the old content and the script would still
// say Done.
func TestMergeRefusesANonObjectConfig(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	script, err := Render("claude-code", ShellPOSIX, "https://api.lazu.ai")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	settings := filepath.Join(home, ".claude/settings.json")
	mustWrite(t, settings, "[]")
	cmd := exec.Command(sh, "-s", "--", "--model", "claude-fable-5-1", "--no-check")
	cmd.Stdin = strings.NewReader(script)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "LAZU_API_KEY=sk-lazu-fake-probe"}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("script reported success against a JSON array:\n%s", out)
	}
	if !strings.Contains(string(out), "not a JSON object") {
		t.Errorf("error does not say what is wrong:\n%s", out)
	}
}

// Codex keeps sub-tables under a provider. Replacing ours has to take the
// whole subtree with it, or a stale [model_providers.lazu.http_headers] rides
// along and its Authorization header breaks every real Codex request.
func TestCodexReplacesTheWholeProviderTable(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	script, err := Render("codex", ShellPOSIX, "https://api.lazu.ai")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	config := filepath.Join(home, ".codex/config.toml")
	mustWrite(t, config, `model = "old"

[model_providers.lazu]
name = "Lazu"
base_url = "https://stale.example.com/v1"

[model_providers.lazu.http_headers]
Authorization = "Bearer sk-stale"

[model_providers.other]
name = "Other"
`)
	cmd := exec.Command(sh, "-s", "--", "--model", "gpt-6-astra", "--no-check")
	cmd.Stdin = strings.NewReader(script)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "LAZU_API_KEY=sk-lazu-fake-probe", "SHELL=/bin/bash"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	written, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	// The file keeps other providers' tables, so it may hold their
	// credentials; the rewrite must not open it to the rest of the machine.
	info, err := os.Stat(config)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("config.toml is mode %#o after the rewrite; want owner-only", mode)
	}
	for _, stale := range []string{"http_headers", "sk-stale", "stale.example.com"} {
		if strings.Contains(string(written), stale) {
			t.Errorf("stale provider setting %q survived:\n%s", stale, written)
		}
	}
	// Somebody else's provider is none of our business.
	if !strings.Contains(string(written), "[model_providers.other]") {
		t.Errorf("another provider's table was dropped:\n%s", written)
	}
}

// fakeCodexPath puts a stand-in `codex` on PATH whose `debug models --bundled`
// prints a one-entry catalog with the fields older Codex versions require,
// so the scripts' cloning is exercised without the real binary.
func fakeCodexPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	const catalog = `{"models":[{"slug":"gpt-bundled","display_name":"GPT","description":"bundled","base_instructions":"the bundled prompt","supports_reasoning_summaries":true,"default_reasoning_level":"low","supported_reasoning_levels":[{"effort":"low","description":"x"}],"shell_type":"unified_exec","visibility":"list","supported_in_api":true,"priority":1,"support_verbosity":true,"truncation_policy":{"mode":"tokens","limit":10000},"experimental_supported_tools":["clock"],"input_modalities":["text","image"],"tool_mode":"code_mode_only","service_tiers":[{"id":"priority","name":"Fast","description":"2x"}],"prefer_websockets":true}]}`
	mustWrite(t, filepath.Join(dir, "codex"), "#!/bin/sh\nif [ \"$1\" = debug ] && [ \"$2\" = models ]; then printf '%s\\n' '"+catalog+"'; exit 0; fi\necho unexpected >&2; exit 1\n")
	if err := os.Chmod(filepath.Join(dir, "codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestCodexCatalogNeedsCodex: without a codex binary the extra models are
// left out and the config still gets written, with no model_catalog_json
// line; a catalog the user keeps elsewhere survives either way, while our
// own stale one is dropped.
func TestCodexCatalogNeedsCodex(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	_, nodeErr := exec.LookPath("node")
	_, pyErr := exec.LookPath("python3")
	if nodeErr != nil && pyErr != nil {
		t.Skip("neither node nor python3 available")
	}
	script, err := Render("codex", ShellPOSIX, "https://api.lazu.ai")
	if err != nil {
		t.Fatal(err)
	}
	run := func(t *testing.T, home string, withCodex bool, args ...string) string {
		t.Helper()
		path := os.Getenv("PATH")
		if withCodex {
			path = fakeCodexPath(t) + ":" + path
		} else {
			// An empty PATH would also hide node/python3; keep a PATH that
			// holds them but drops the real codex, by rendering it first.
			bin := t.TempDir()
			for _, tool := range []string{"node", "python3", "awk", "sed", "grep", "tr", "cp", "mv", "rm", "mkdir", "chmod", "date", "basename", "dirname", "touch", "cat", "curl", "stty"} {
				if p, err := exec.LookPath(tool); err == nil {
					_ = os.Symlink(p, filepath.Join(bin, tool))
				}
			}
			path = bin
		}
		cmd := exec.Command(sh, append([]string{"-s", "--", "--key", "sk-lazu-test0123456789", "--no-check"}, args...)...)
		cmd.Stdin = strings.NewReader(script)
		cmd.Env = []string{"HOME=" + home, "PATH=" + path, "SHELL=/bin/zsh"}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("script failed: %v\n%s", err, out)
		}
		data, err := os.ReadFile(filepath.Join(home, ".codex/config.toml"))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	t.Run("no codex on PATH", func(t *testing.T) {
		home := t.TempDir()
		config := run(t, home, false, "--model", "gpt-5.2", "--also", "kimi-k3")
		if strings.Contains(config, "model_catalog_json") {
			t.Errorf("model_catalog_json written without a catalog:\n%s", config)
		}
		if _, err := os.Stat(filepath.Join(home, ".codex/lazu-models.json")); err == nil {
			t.Error("catalog file written without codex")
		}
	})
	t.Run("user catalog kept, ours replaced", func(t *testing.T) {
		home := t.TempDir()
		mustWrite(t, filepath.Join(home, ".codex/config.toml"), "model_catalog_json = \"/srv/mine.json\"\n")
		config := run(t, home, false, "--model", "gpt-5.2")
		if !strings.Contains(config, `model_catalog_json = "/srv/mine.json"`) {
			t.Errorf("user's model_catalog_json dropped:\n%s", config)
		}
		mustWrite(t, filepath.Join(home, ".codex/config.toml"), "model_catalog_json = \"/old/.codex/lazu-models.json\"\n")
		config = run(t, home, false, "--model", "gpt-5.2")
		if strings.Contains(config, "model_catalog_json") {
			t.Errorf("stale Lazu model_catalog_json kept:\n%s", config)
		}
		config = run(t, home, true, "--model", "gpt-5.2", "--also", "kimi-k3")
		if strings.Count(config, "model_catalog_json") != 1 || !strings.Contains(config, "/.codex/lazu-models.json\"") {
			t.Errorf("catalog line not written once:\n%s", config)
		}
	})
	t.Run("glob in a model id stays literal", func(t *testing.T) {
		home := t.TempDir()
		run(t, home, true, "--model", "gpt-5.2", "--also", "*")
		data, err := os.ReadFile(filepath.Join(home, ".codex/lazu-models.json"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"slug": "*"`) || strings.Contains(string(data), `"slug": "config.toml"`) {
			t.Errorf("--also '*' was expanded:\n%s", data)
		}
	})
}
