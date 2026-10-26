package channel

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Protocol Layer rule 3 (AGENTS.md): what a model can do comes from the
// capability snapshot, never from its name. This test fails on a new
// `strings.HasPrefix(<model name>, "...")` or `strings.Contains(<model
// name>, "...")` anywhere under internal/relay/channel. The entries below
// are the debt that existed when the rule got teeth (2026-10-20); each is
// removed as its code moves to the catalog, and none may be added.
var modelNameJudgementDebt = map[string]int{
	// Completion-API selection for the retired claude-2 and claude-instant
	// lines; an endpoint-shape fact that belongs on the catalog row.
	"claude/adaptor.go": 2,
	// imagen / embedding routing by name.
	"gemini/adaptor.go": 9,
	// o-series / gpt-5 parameter rewrites keyed on the model line.
	"openai/adaptor.go":        5,
	"openai/native_request.go": 5,
	// grok-3-mini reasoning_effort handling.
	"xai/adaptor.go": 1,
}

// modelNameJudgement is a HasPrefix/Contains whose subject is a variable
// named after a model (model, modelName, request.Model, info.UpstreamModelName).
var modelNameJudgement = regexp.MustCompile(`strings\.(?:HasPrefix|Contains)\(\s*[a-zA-Z_.]*[mM]odel[a-zA-Z_.]*\s*,`)

func TestNoNewModelNameJudgementsInChannels(t *testing.T) {
	found := map[string]int{}
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			found[filepath.ToSlash(path)] += len(modelNameJudgement.FindAllString(trimmed, -1))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for file, count := range found {
		if count == 0 {
			continue
		}
		allowed := modelNameJudgementDebt[file]
		if count > allowed {
			t.Errorf("%s: %d model-name judgements, %d recorded as debt; what a model can do comes from the capability snapshot (AGENTS.md, Protocol Layer 3)", file, count, allowed)
		}
	}
	for file, allowed := range modelNameJudgementDebt {
		if found[file] < allowed {
			t.Errorf("%s: debt entry says %d, found %d; lower the entry so the debt only shrinks", file, allowed, found[file])
		}
	}
}
