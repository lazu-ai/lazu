package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultBenchmarkBodyPreservesHistoricalBytes(t *testing.T) {
	// Frozen pre-option payloads for gpt-mock. These also retain the historical
	// byte-size overshoot from completing the final generated word.
	for _, fixture := range []struct {
		api    string
		stream bool
		kb     int
		bytes  int
		sha256 string
	}{
		{"responses", false, 1, 1149, "927637e43c0af45da6d66e42a99a36a65e2686c63e7df85c9530ba4564b990e2"},
		{"responses", false, 100, 102522, "dad5dc8ab749c5993a1d71537fae951a3b9414a0abb18ca0efded9e91a85123c"},
		{"responses", false, 600, 614530, "4fb41aa24d3c337b537afde959c06ec6e57b8b5e4adc94b84551f24d246b3c60"},
		{"responses", true, 1, 1148, "a4ff1f2fa5a65ab2307d682e8d93d9c36093b2a6e6333f006256469ffbcaf708"},
		{"responses", true, 100, 102521, "90a6ceaedd07a0fa5034554a703569c52ee808abe9017d3d92896b537d295b75"},
		{"responses", true, 600, 614529, "6fa546c47b046e3b3945ad780491d28120556c0d20c751d32c3162ee8e49eeca"},
		{"chat", false, 1, 1104, "c36ab6469c5f9d89365549bf2b44dbd081386470f060a3c776cbdd03f318a49c"},
		{"chat", false, 100, 102477, "f386e8d7c468ca264b01c8730b459458d736405ef6549d17f5965ff07bf8ea4b"},
		{"chat", false, 600, 614485, "c68395eeb59ab38977967310abda48fdbc6ef515f9991d3f4869e4b4c1313e6b"},
		{"chat", true, 1, 1143, "4f087879ea5f199aba8ac2548c95f8b16fd95435e3e353774497a5fc337c76bc"},
		{"chat", true, 100, 102516, "199f2f7fc5ae565b7b60473dadedb5df6a667dec28477c29661ad78d4b9d7624"},
		{"chat", true, 600, 614524, "f536e35d63f7ca716c409be6f7651115862f0f2585941a54213f87ddc0983deb"},
	} {
		body := makeBenchmarkBody(fixture.api, "gpt-mock", fixture.stream, fixture.kb, "")
		payload := body.payload(9, 99)
		digest := sha256.Sum256(payload)
		if len(payload) != fixture.bytes || hex.EncodeToString(digest[:]) != fixture.sha256 {
			t.Fatalf("%s/stream=%v/%dKB default bytes changed: len=%d sha256=%x", fixture.api, fixture.stream, fixture.kb, len(payload), digest)
		}
		if !bytes.Equal(payload, body.payload(0, 0)) || !json.Valid(payload) {
			t.Fatal("default body depends on request identity or is not valid JSON")
		}
		t.Logf("historical payload %s stream=%v kb=%d bytes=%d sha256=%x", fixture.api, fixture.stream, fixture.kb, len(payload), digest)
	}
}

func promptAndContract(t *testing.T, api string, payload []byte, stream bool) string {
	t.Helper()
	var value map[string]json.RawMessage
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	if string(value["model"]) != `"gpt-mock"` || string(value["stream"]) != map[bool]string{true: "true", false: "false"}[stream] {
		t.Fatal("model/stream contract changed")
	}
	if api == "responses" {
		if len(value) != 3 {
			t.Fatal("unexpected top-level request fields")
		}
		var input []struct {
			Type, Role string
			Content    []struct{ Type, Text string }
		}
		if err := json.Unmarshal(value["input"], &input); err != nil || len(input) != 1 || input[0].Type != "message" || input[0].Role != "user" || len(input[0].Content) != 1 || input[0].Content[0].Type != "input_text" {
			t.Fatal("Responses prompt contract changed")
		}
		return input[0].Content[0].Text
	}
	var messages []struct{ Role, Content string }
	if err := json.Unmarshal(value["messages"], &messages); err != nil || len(messages) != 1 || messages[0].Role != "user" {
		t.Fatal("Chat prompt contract changed")
	}
	if stream {
		if len(value) != 4 || string(value["stream_options"]) != `{"include_usage":true}` {
			t.Fatal("stream usage fixture contract changed")
		}
	} else if len(value) != 3 {
		t.Fatal("unexpected top-level request fields")
	}
	return messages[0].Content
}

func TestUniqueBenchmarkPromptsChangeActualTextPerRequestAndRun(t *testing.T) {
	for _, api := range []string{"responses", "chat"} {
		for _, stream := range []bool{true, false} {
			base := makeBenchmarkBody(api, "gpt-mock", stream, 1, "")
			body := makeBenchmarkBody(api, "gpt-mock", stream, 1, strings.Repeat("a", 32))
			differentRun := makeBenchmarkBody(api, "gpt-mock", stream, 1, strings.Repeat("b", 32))
			original := promptAndContract(t, api, base.payload(0, 0), stream)
			seen := map[string]bool{}
			for _, payload := range [][]byte{body.payload(0, 0), body.payload(0, 1), body.payload(1, 0), differentRun.payload(0, 0)} {
				prompt := promptAndContract(t, api, payload, stream)
				if seen[prompt] || !strings.HasPrefix(prompt, original) || !strings.Contains(prompt, "[lazu-bench:") || len(payload) <= len(base.text) {
					t.Fatal("prompt was reused, lost fixture text, or placed identity outside actual prompt")
				}
				seen[prompt] = true
			}
		}
	}
}

var benchmarkPayloadSink []byte

func TestUniqueBenchmarkBodyUsesOnePayloadAllocation(t *testing.T) {
	body := makeBenchmarkBody("responses", "gpt-mock", true, 600, strings.Repeat("a", 32))
	allocations := testing.AllocsPerRun(10, func() { benchmarkPayloadSink = body.payload(123, 456) })
	if allocations != 1 {
		t.Fatalf("large unique body needs %g allocations, want one", allocations)
	}
	if !json.Valid(benchmarkPayloadSink) {
		t.Fatal("large unique payload invalid")
	}
}

func TestBenchmarkRunNonceIsFreshHex(t *testing.T) {
	first, err := benchmarkRunNonce()
	if err != nil {
		t.Fatal(err)
	}
	second, err := benchmarkRunNonce()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := hex.DecodeString(first)
	if err != nil || len(decoded) != 16 || first == second {
		t.Fatal("nonce must be a fresh 128-bit JSON-safe hex value")
	}
}
