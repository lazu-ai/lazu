package router

import (
	"context"
	"reflect"
	"testing"
)

func TestExtractHintsReturnsNilForLegacyRequests(t *testing.T) {
	// A plain OpenAI request with no lazu-specific fields must
	// produce nil Hints. The rest of the pipeline relies on this
	// "no hints == legacy caller" sentinel.
	body := []byte(`{
        "model": "gpt-4o",
        "messages": [{"role": "user", "content": "hi"}]
    }`)
	f := ExtractFeatures(context.Background(), nil, body)
	if f.Hints != nil {
		t.Fatalf("legacy request produced non-nil Hints: %+v", f.Hints)
	}
}

func TestExtractHintsFromLazuNamespace(t *testing.T) {
	body := []byte(`{
        "model": "gpt-4o",
        "messages": [{"role": "user", "content": "hi"}],
        "metadata": {
            "lazu": {
                "intent": "code",
                "budget_tier": "cheap",
                "preferred_provider": "anthropic",
                "session_id": "sess-42",
                "fallback_models": ["claude-sonnet-4-6", "gpt-4o-mini"]
            },
            "helicone": {"property": "ignored"}
        }
    }`)
	f := ExtractFeatures(context.Background(), nil, body)
	if f.Hints == nil {
		t.Fatal("expected non-nil Hints")
	}
	if f.Hints.Intent != "code" {
		t.Fatalf("Intent=%q", f.Hints.Intent)
	}
	if f.Hints.BudgetTier != "cheap" {
		t.Fatalf("BudgetTier=%q", f.Hints.BudgetTier)
	}
	if f.Hints.PreferredProvider != "anthropic" {
		t.Fatalf("PreferredProvider=%q", f.Hints.PreferredProvider)
	}
	if f.Hints.SessionID != "sess-42" {
		t.Fatalf("SessionID=%q", f.Hints.SessionID)
	}
	want := []string{"claude-sonnet-4-6", "gpt-4o-mini"}
	if !reflect.DeepEqual(f.Hints.FallbackModels, want) {
		t.Fatalf("FallbackModels=%v want=%v", f.Hints.FallbackModels, want)
	}
}

func TestExtractHintsFromTopLevelMetadata(t *testing.T) {
	// Callers that don't bother with the lazu sub-namespace can put
	// hints directly under metadata.* — accept that too.
	body := []byte(`{
        "model": "gpt-4o",
        "messages": [{"role": "user", "content": "hi"}],
        "metadata": {
            "intent": "math",
            "session_id": "sess-99"
        }
    }`)
	f := ExtractFeatures(context.Background(), nil, body)
	if f.Hints == nil {
		t.Fatal("expected non-nil Hints")
	}
	if f.Hints.Intent != "math" {
		t.Fatalf("Intent=%q", f.Hints.Intent)
	}
	if f.Hints.SessionID != "sess-99" {
		t.Fatalf("SessionID=%q", f.Hints.SessionID)
	}
}

func TestExtractHintsLazuOverridesTopLevel(t *testing.T) {
	// Same key in both locations: lazu namespace wins (called first).
	body := []byte(`{
        "metadata": {
            "intent": "loser",
            "lazu": {"intent": "winner"}
        }
    }`)
	f := ExtractFeatures(context.Background(), nil, body)
	if f.Hints == nil || f.Hints.Intent != "winner" {
		t.Fatalf("lazu namespace must take precedence; got=%+v", f.Hints)
	}
}

func TestExtractHintsDoesNotPromoteOpenAIUserToSessionID(t *testing.T) {
	// The OpenAI `user` field names an end user, not an agent session;
	// session identity is resolved by the agent-session middleware.
	body := []byte(`{
        "model": "gpt-4o",
        "messages": [],
        "user": "end-user-abc"
    }`)
	f := ExtractFeatures(context.Background(), nil, body)
	if f.Hints != nil {
		t.Fatalf("`user` must not produce routing hints, got=%+v", f.Hints)
	}
}

func TestExtractHintsExplicitSessionIDBeatsUserField(t *testing.T) {
	// When both `metadata.lazu.session_id` and `user` are present,
	// the explicit hint wins — the OpenAI `user` field is a fallback,
	// not an override.
	body := []byte(`{
        "user": "openai-user",
        "metadata": {"lazu": {"session_id": "lazu-session"}}
    }`)
	f := ExtractFeatures(context.Background(), nil, body)
	if f.Hints == nil || f.Hints.SessionID != "lazu-session" {
		t.Fatalf("explicit session_id should win, got=%+v", f.Hints)
	}
}

func TestExtractHintsRejectsEmptyStrings(t *testing.T) {
	// Empty-string fields must not mark Hints as "set"; they are
	// indistinguishable from absent fields semantically.
	body := []byte(`{
        "metadata": {"lazu": {"intent": "", "session_id": ""}}
    }`)
	f := ExtractFeatures(context.Background(), nil, body)
	if f.Hints != nil {
		t.Fatalf("empty-string hints should produce nil, got=%+v", f.Hints)
	}
}

func TestExtractHintsRejectsNonStringFallbackEntries(t *testing.T) {
	// Mixed-type arrays (e.g. numbers in fallback_models) should be
	// filtered, not crash.
	body := []byte(`{
        "metadata": {"lazu": {"fallback_models": ["gpt-4o", 42, true, "claude"]}}
    }`)
	f := ExtractFeatures(context.Background(), nil, body)
	if f.Hints == nil {
		t.Fatal("expected non-nil Hints")
	}
	want := []string{"gpt-4o", "claude"}
	if !reflect.DeepEqual(f.Hints.FallbackModels, want) {
		t.Fatalf("FallbackModels=%v want=%v", f.Hints.FallbackModels, want)
	}
}

func TestExtractHintsMalformedMetadataDoesNotCrash(t *testing.T) {
	// metadata typed as something other than an object should not panic.
	bodies := [][]byte{
		[]byte(`{"metadata": "not an object"}`),
		[]byte(`{"metadata": 42}`),
		[]byte(`{"metadata": null}`),
		[]byte(`{"metadata": ["array"]}`),
	}
	for _, body := range bodies {
		f := ExtractFeatures(context.Background(), nil, body)
		if f.Hints != nil {
			t.Fatalf("malformed metadata %q produced unexpected Hints=%+v", body, f.Hints)
		}
	}
}

func TestExtractHintsCoexistsWithOtherFeatures(t *testing.T) {
	// Pulling hints must not break the other feature extraction.
	body := []byte(`{
        "model": "gpt-4o",
        "stream": true,
        "tools": [{"type": "function", "function": {"name": "f"}}],
        "messages": [
            {"role": "user", "content": [
                {"type": "text", "text": "describe this"},
                {"type": "image_url", "image_url": {"url": "http://x/y.png"}}
            ]}
        ],
        "metadata": {"lazu": {"intent": "vision"}}
    }`)
	f := ExtractFeatures(context.Background(), nil, body)
	if !f.HasVision {
		t.Fatal("HasVision dropped")
	}
	if f.Hints == nil || f.Hints.Intent != "vision" {
		t.Fatalf("hints lost or wrong: %+v", f.Hints)
	}
}
