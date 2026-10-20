//go:build record

package contracttest

// Records real upstream streams as fixtures for the codec work
// (AGENTS.md, Protocol Layer rule 8: stream tests use recorded upstream SSE).
//
//	ANTHROPIC_API_KEY=... DEEPSEEK_API_KEY=... GEMINI_API_KEY=... \
//	  go test -tags record -run TestRecordUpstreamFixtures ./internal/relay/contracttest/
//
// Each vendor whose key is set gets its cases written under
// internal/protocol/textgen/testdata/upstream/<vendor>/<case>.sse, the body exactly as the vendor sent
// it, one SSE event per block. Keys are read from the environment only and
// never written; the files hold no credentials (vendor request ids are not
// secrets). A vendor whose key is absent is skipped, not failed.
//
// RECORD_ONLY=<vendor> or <vendor>/<case> re-records a subset.
//
// With LAZU_API_KEY set (and optionally LAZU_BASE_URL, default
// https://api.lazu.ai) every case is sent through the lazu gateway instead,
// on the same wire API the vendor speaks (/v1/messages, /v1/chat/completions,
// /v1/responses, /v1beta/models/<m>:streamGenerateContent): on a native
// channel the gateway passes the vendor's stream through, so the recording
// is the vendor's shape as served, under testdata/upstream/<vendor>/ too.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type upstreamCase struct {
	vendor, name, url, auth string
	headers                 map[string]string
	body                    string
}

var upstreamCases = []upstreamCase{
	// Anthropic Messages: text, thinking (adaptive), tool call, json_schema
	{vendor: "anthropic", name: "text", url: "https://api.anthropic.com/v1/messages", auth: "x-api-key",
		body: `{"model":"claude-haiku-5-5","max_tokens":200,"stream":true,"messages":[{"role":"user","content":"Say hello in five words."}]}`},
	{vendor: "anthropic", name: "thinking_adaptive", url: "https://api.anthropic.com/v1/messages", auth: "x-api-key",
		body: `{"model":"claude-haiku-5-5","max_tokens":1200,"stream":true,"thinking":{"type":"adaptive","display":"summarized"},"output_config":{"effort":"low"},"messages":[{"role":"user","content":"What is 17*23? Think briefly."}]}`},
	{vendor: "anthropic", name: "thinking_block", url: "https://api.anthropic.com/v1/messages", auth: "x-api-key",
		body: `{"model":"claude-haiku-5-5","max_tokens":4000,"stream":true,"thinking":{"type":"adaptive","display":"summarized"},"output_config":{"effort":"high"},"messages":[{"role":"user","content":"A train leaves at 9:40 and arrives at 13:05 after a 25 minute stop. How long was it moving? Reason it through before answering."}]}`},
	{vendor: "anthropic", name: "tool_call", url: "https://api.anthropic.com/v1/messages", auth: "x-api-key",
		body: `{"model":"claude-haiku-5-5","max_tokens":300,"stream":true,"tools":[{"name":"get_weather","description":"Get the weather for a city","input_schema":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}],"tool_choice":{"type":"any"},"messages":[{"role":"user","content":"Weather in Paris?"}]}`},
	{vendor: "anthropic", name: "json_schema", url: "https://api.anthropic.com/v1/messages", auth: "x-api-key",
		body: `{"model":"claude-haiku-5-5","max_tokens":200,"stream":true,"output_config":{"format":{"type":"json_schema","schema":{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}}},"messages":[{"role":"user","content":"Title this chat about protocol gateways."}]}`},
	// DeepSeek Chat and Responses
	{vendor: "deepseek", name: "chat_text", url: "https://api.deepseek.com/v1/chat/completions", auth: "bearer",
		body: `{"model":"deepseek-flash","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"Say hello in five words."}]}`},
	{vendor: "deepseek", name: "chat_tool_call", url: "https://api.deepseek.com/v1/chat/completions", auth: "bearer",
		body: `{"model":"deepseek-flash","stream":true,"stream_options":{"include_usage":true},"reasoning_effort":"none","tools":[{"type":"function","function":{"name":"get_weather","description":"Get the weather for a city","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}],"tool_choice":"required","messages":[{"role":"user","content":"Weather in Paris?"}]}`},
	{vendor: "deepseek", name: "chat_json_schema_refused", url: "https://api.deepseek.com/v1/chat/completions", auth: "bearer",
		body: `{"model":"deepseek-flash","stream":false,"response_format":{"type":"json_schema","json_schema":{"name":"session_title","strict":true,"schema":{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}}},"messages":[{"role":"user","content":"Title this chat about protocol gateways."}]}`},
	{vendor: "deepseek", name: "responses_text", url: "https://api.deepseek.com/v1/responses", auth: "bearer",
		body: `{"model":"deepseek-flash","stream":true,"store":false,"input":"Say hello in five words."}`},
	{vendor: "deepseek", name: "responses_json_schema", url: "https://api.deepseek.com/v1/responses", auth: "bearer",
		body: `{"model":"deepseek-flash","stream":true,"store":false,"reasoning":{"effort":"none"},"text":{"format":{"type":"json_schema","name":"session_title","schema":{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}}},"input":"Title this chat about protocol gateways."}`},
	{vendor: "deepseek", name: "responses_tool_call", url: "https://api.deepseek.com/v1/responses", auth: "bearer",
		// thinking on refuses a forced tool_choice ("Thinking mode does not support
		// this tool_choice", recorded 2026-10-21), so reasoning is off here
		body: `{"model":"deepseek-flash","stream":true,"store":false,"reasoning":{"effort":"none"},"tools":[{"type":"function","name":"get_weather","description":"Get the weather for a city","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}],"tool_choice":"required","input":"Weather in Paris?"}`},
	{vendor: "deepseek", name: "responses_tool_choice_refused_with_thinking", url: "https://api.deepseek.com/v1/responses", auth: "bearer",
		body: `{"model":"deepseek-flash","stream":false,"store":false,"tools":[{"type":"function","name":"get_weather","description":"Get the weather for a city","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}],"tool_choice":"required","input":"Weather in Paris?"}`},
	// Gemini generateContent (streaming, SSE)
	{vendor: "gemini", name: "text", url: "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.5-flash:streamGenerateContent?alt=sse", auth: "x-goog-api-key",
		body: `{"contents":[{"role":"user","parts":[{"text":"Say hello in five words."}]}]}`},
	{vendor: "gemini", name: "tool_call", url: "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.5-flash:streamGenerateContent?alt=sse", auth: "x-goog-api-key",
		body: `{"contents":[{"role":"user","parts":[{"text":"Weather in Paris?"}]}],"tools":[{"functionDeclarations":[{"name":"get_weather","description":"Get the weather for a city","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}]}],"toolConfig":{"functionCallingConfig":{"mode":"ANY"}}}`},
	{vendor: "gemini", name: "json_schema", url: "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.5-flash:streamGenerateContent?alt=sse", auth: "x-goog-api-key",
		body: `{"contents":[{"role":"user","parts":[{"text":"Title this chat about protocol gateways."}]}],"generationConfig":{"responseMimeType":"application/json","responseSchema":{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}}}`},
}

var vendorKeys = map[string]string{"anthropic": "ANTHROPIC_API_KEY", "deepseek": "DEEPSEEK_API_KEY", "gemini": "GEMINI_API_KEY"}

// throughLazu rewrites a vendor URL to the lazu gateway's route for the same
// wire API.
func throughLazu(base, vendorURL string) string {
	base = strings.TrimRight(base, "/")
	switch {
	case strings.Contains(vendorURL, "/v1/messages"):
		return base + "/v1/messages"
	case strings.Contains(vendorURL, "/v1/chat/completions"):
		return base + "/v1/chat/completions"
	case strings.Contains(vendorURL, "/v1/responses"):
		return base + "/v1/responses"
	case strings.Contains(vendorURL, "/v1beta/models/"):
		return base + vendorURL[strings.Index(vendorURL, "/v1beta/models/"):]
	}
	return vendorURL
}

func TestRecordUpstreamFixtures(t *testing.T) {
	lazuKey := os.Getenv("LAZU_API_KEY")
	if os.Getenv("RECORD_UPSTREAM") == "" && lazuKey == "" && os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("DEEPSEEK_API_KEY") == "" && os.Getenv("GEMINI_API_KEY") == "" {
		t.Skip("no vendor key in the environment")
	}
	lazuBase := os.Getenv("LAZU_BASE_URL")
	if lazuBase == "" {
		lazuBase = "https://api.lazu.ai"
	}
	client := &http.Client{Timeout: 90 * time.Second}
	only := os.Getenv("RECORD_ONLY") // "vendor" or "vendor/case", to re-record a few
	for _, tc := range upstreamCases {
		if only != "" && only != tc.vendor && only != tc.vendor+"/"+tc.name {
			continue
		}
		key, url := os.Getenv(vendorKeys[tc.vendor]), tc.url
		if lazuKey != "" {
			key, url = lazuKey, throughLazu(lazuBase, tc.url)
		}
		if key == "" {
			t.Logf("%s/%s: skipped, %s not set", tc.vendor, tc.name, vendorKeys[tc.vendor])
			continue
		}
		t.Run(tc.vendor+"/"+tc.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			switch {
			case lazuKey != "":
				// lazu takes the key on every wire API as a bearer token and
				// as the vendor's own header alike
				req.Header.Set("Authorization", "Bearer "+key)
				req.Header.Set("x-api-key", key)
				req.Header.Set("x-goog-api-key", key)
				if tc.auth == "x-api-key" {
					req.Header.Set("anthropic-version", "2023-06-01")
				}
			case tc.auth == "bearer":
				req.Header.Set("Authorization", "Bearer "+key)
			case tc.auth == "x-api-key":
				req.Header.Set("x-api-key", key)
				req.Header.Set("anthropic-version", "2023-06-01")
			default:
				req.Header.Set(tc.auth, key)
			}
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join("..", "..", "protocol", "textgen", "testdata", "upstream", tc.vendor)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			ext := ".sse"
			if !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
				ext = ".json"
			}
			path := filepath.Join(dir, tc.name+ext)
			var out bytes.Buffer
			fmt.Fprintf(&out, "# recorded %s from %s, HTTP %d, content-type %s\n# request: %s\n", time.Now().UTC().Format(time.RFC3339), url, res.StatusCode, res.Header.Get("Content-Type"), tc.body)
			sc := bufio.NewScanner(bytes.NewReader(body))
			sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
			for sc.Scan() {
				out.WriteString(sc.Text())
				out.WriteByte('\n')
			}
			if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: HTTP %d, %d bytes", path, res.StatusCode, len(body))
		})
	}
}
