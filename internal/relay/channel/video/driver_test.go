package video

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lazu-ai/lazu/internal/dto"
)

func TestSelectPicksFamilyFromChannelAndModel(t *testing.T) {
	cases := []struct {
		name   string
		ch     Channel
		models []string
		want   string
	}{
		{"openai type defaults to gateway", Channel{Type: channelTypeOpenAI}, []string{"veo-3.1"}, dto.VideoFamilyGateway},
		{"seedance on openai type is ark", Channel{Type: channelTypeOpenAI}, []string{"doubao-seedance-1-5-pro-251215"}, dto.VideoFamilyArk},
		{"mapped upstream name decides too", Channel{Type: channelTypeOpenAI}, []string{"video-pro", "dreamina-seedance-2-0-260128"}, dto.VideoFamilyArk},
		{"gemini type is veo", Channel{Type: channelTypeGemini}, []string{"veo-3.1"}, dto.VideoFamilyGemini},
		{"custom type is envelope", Channel{Type: channelTypeCustom}, []string{"any"}, dto.VideoFamilyEnvelope},
		{"explicit family wins", Channel{Type: channelTypeOpenAI, Settings: dto.ChannelOtherSettings{VideoFamily: dto.VideoFamilyEnvelope}}, []string{"seedance-2.0"}, dto.VideoFamilyEnvelope},
		{"envelope config implies envelope", Channel{Type: channelTypeOpenAI, Settings: dto.ChannelOtherSettings{VideoEnvelope: &dto.VideoEnvelopeConfig{Preset: "tasks"}}}, []string{"veo3.1"}, dto.VideoFamilyEnvelope},
	}
	for _, tc := range cases {
		driver, err := Select(tc.ch, tc.models...)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if driver.Family() != tc.want {
			t.Fatalf("%s: family=%s want %s", tc.name, driver.Family(), tc.want)
		}
	}
	if _, err := Select(Channel{Settings: dto.ChannelOtherSettings{VideoFamily: "kling"}}, "x"); err == nil {
		t.Fatal("unknown family must be refused")
	}
}

func TestGatewayLifecycle(t *testing.T) {
	ch := Channel{Type: channelTypeOpenAI, BaseURL: "https://gateway.ai.cloudflare.com/account/gateway/openai", Key: "secret", HeaderOverride: map[string]any{"X-Extra": "v {api_key}"}}
	driver := gatewayDriver{}
	seed := int64(42)
	submit, err := driver.BuildSubmit(ch, dto.VideoRequest{Model: "veo-3.1", Prompt: "waves", Seconds: 4, Size: "1280x720", Seed: &seed,
		FrameImages: []dto.VideoFrameImage{{URL: "https://x/first.png", FrameType: "first_frame"}}}, "veo-3.1")
	if err != nil {
		t.Fatalf("BuildSubmit: %v", err)
	}
	if submit.URL != "https://gateway.ai.cloudflare.com/account/gateway/openai/videos" {
		t.Fatalf("cloudflare gateway submit url: %s", submit.URL)
	}
	if submit.Headers.Get("Authorization") != "Bearer secret" || submit.Headers.Get("X-Extra") != "v secret" {
		t.Fatalf("headers: %+v", submit.Headers)
	}
	var body map[string]any
	if err := json.Unmarshal(submit.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["seconds"] != float64(4) || body["seed"] != float64(42) || body["frame_images"] == nil {
		t.Fatalf("body: %+v", body)
	}
	receipt, err := driver.ParseSubmit(ch, []byte(`{"id":"video_1","status":"queued"}`))
	if err != nil || receipt.UpstreamID != "video_1" {
		t.Fatalf("ParseSubmit: %+v %v", receipt, err)
	}
	if _, err := driver.ParseSubmit(ch, []byte(`{"status":"queued"}`)); err == nil {
		t.Fatal("missing id must fail")
	}
	status, err := driver.BuildStatus(ch, Job{UpstreamID: "video/42"})
	if err != nil || status.URL != "https://gateway.ai.cloudflare.com/account/gateway/openai/videos/video%2F42" {
		t.Fatalf("status url: %+v %v", status, err)
	}
	pending, _ := driver.ParseStatus(ch, []byte(`{"status":"in_progress"}`))
	if pending.State != StateRunning {
		t.Fatalf("pending: %+v", pending)
	}
	done, _ := driver.ParseStatus(ch, []byte(`{"status":"completed","video":{"url":"https://cdn.example.com/v.mp4"},"seconds":"8","usage":{"cost":0.42}}`))
	if done.State != StateSucceeded || done.ArtifactURL != "https://cdn.example.com/v.mp4" || done.Facts == nil || done.Facts.Seconds != 8 || done.Facts.ProviderCostUSD == nil || !done.Facts.Reported {
		t.Fatalf("completed: %+v facts=%+v", done, done.Facts)
	}
	failed, _ := driver.ParseStatus(ch, []byte(`{"status":"failed","error":{"code":"moderation_blocked","message":"blocked"}}`))
	if failed.State != StateFailed || failed.ErrorCode != "moderation_blocked" {
		t.Fatalf("failed: %+v", failed)
	}
	download, err := driver.BuildDownload(ch, Job{UpstreamID: "video/42"}, "")
	if err != nil || !download.Authenticated || download.URL != "https://gateway.ai.cloudflare.com/account/gateway/openai/videos/video%2F42/content" {
		t.Fatalf("content download: %+v %v", download, err)
	}
	external, err := driver.BuildDownload(Channel{Type: channelTypeOpenAI, BaseURL: "https://api.example.com", Key: "k"}, Job{UpstreamID: "job-1"}, "https://cdn.example.net/video.mp4")
	if err != nil || external.Authenticated || external.Headers.Get("Authorization") != "" {
		t.Fatalf("external artifact must not carry the key: %+v %v", external, err)
	}
	internal, err := driver.BuildDownload(Channel{Type: channelTypeOpenAI, BaseURL: "https://api.example.com", Key: "k"}, Job{UpstreamID: "job-1"}, "/artifacts/video.mp4")
	if err != nil || !internal.Authenticated || internal.URL != "https://api.example.com/artifacts/video.mp4" {
		t.Fatalf("same-origin artifact keeps the key: %+v %v", internal, err)
	}
	facts := driver.EstimateFacts([]byte(`{"seconds":7,"quality":"hd","size":"1280x720"}`), nil)
	if facts.Seconds != 7 || facts.Resolution != "720p" || facts.Ratio != "16:9" {
		t.Fatalf("estimate: %+v", facts)
	}
}

func TestArkLifecycle(t *testing.T) {
	ch := Channel{Type: channelTypeOpenAI, BaseURL: "https://gateway.example/ark/api/v3", Key: "ark-key"}
	driver := arkDriver{}
	audio := true
	submit, err := driver.BuildSubmit(ch, dto.VideoRequest{
		Model: "doubao-seedance-1-5-pro-251215", Prompt: "a fox", Seconds: 5, AspectRatio: "16:9", Resolution: "720p", GenerateAudio: &audio,
		FrameImages: []dto.VideoFrameImage{{URL: "https://x/a.png", FrameType: "first_frame"}, {URL: "https://x/b.png", FrameType: "last_frame"}},
		InputRefs:   []dto.VideoInputReference{{URL: "https://x/ref.mp4", Type: "video"}},
	}, "doubao-seedance-1-5-pro-251215")
	if err != nil {
		t.Fatalf("BuildSubmit: %v", err)
	}
	if submit.URL != "https://gateway.example/ark/api/v3/contents/generations/tasks" {
		t.Fatalf("submit url: %s", submit.URL)
	}
	var body struct {
		Model   string `json:"model"`
		Content []struct {
			Type string `json:"type"`
			Role string `json:"role"`
		} `json:"content"`
		Ratio         string `json:"ratio"`
		Duration      int    `json:"duration"`
		Resolution    string `json:"resolution"`
		GenerateAudio bool   `json:"generate_audio"`
	}
	if err := json.Unmarshal(submit.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Duration != 5 || body.Ratio != "16:9" || body.Resolution != "720p" || !body.GenerateAudio || len(body.Content) != 4 ||
		body.Content[1].Role != "first_frame" || body.Content[2].Role != "last_frame" || body.Content[3].Type != "video_url" {
		t.Fatalf("ark body: %+v", body)
	}
	receipt, err := driver.ParseSubmit(ch, []byte(`{"id":"cgt-2026-abc"}`))
	if err != nil || receipt.UpstreamID != "cgt-2026-abc" {
		t.Fatalf("ParseSubmit: %+v %v", receipt, err)
	}
	status, err := driver.BuildStatus(ch, Job{UpstreamID: "cgt-2026-abc"})
	if err != nil || status.URL != "https://gateway.example/ark/api/v3/contents/generations/tasks/cgt-2026-abc" || status.Headers.Get("Authorization") != "Bearer ark-key" {
		t.Fatalf("status: %+v %v", status, err)
	}
	done, err := driver.ParseStatus(ch, []byte(`{"id":"cgt","status":"succeeded","content":{"video_url":"https://ark-content.tos.volces.com/x.mp4"},"usage":{"completion_tokens":108900,"total_tokens":108900},"resolution":"720p","ratio":"16:9","duration":5,"framespersecond":24,"generate_audio":true}`))
	if err != nil || done.State != StateSucceeded || done.ArtifactURL == "" || done.Facts == nil ||
		done.Facts.OutputTokens != 108900 || done.Facts.Seconds != 5 || done.Facts.Resolution != "720p" || done.Facts.FPS != 24 || !done.Facts.Audio || !done.Facts.Reported {
		t.Fatalf("succeeded: %+v facts=%+v err=%v", done, done.Facts, err)
	}
	running, _ := driver.ParseStatus(ch, []byte(`{"status":"running"}`))
	queued, _ := driver.ParseStatus(ch, []byte(`{"status":"queued"}`))
	if running.State != StateRunning || queued.State != StateQueued {
		t.Fatalf("states: %+v %+v", running, queued)
	}
	failed, _ := driver.ParseStatus(ch, []byte(`{"status":"failed","error":{"code":"SensitiveContent","message":"nope"}}`))
	if failed.State != StateFailed || failed.ErrorCode != "SensitiveContent" {
		t.Fatalf("failed: %+v", failed)
	}
	download, err := driver.BuildDownload(ch, Job{UpstreamID: "cgt"}, "https://ark-content.tos.volces.com/x.mp4")
	if err != nil || download.Authenticated {
		t.Fatalf("TOS artifact must not carry the key: %+v %v", download, err)
	}
	facts := driver.EstimateFacts(submit.Body, DefaultCapabilities("doubao-seedance-1-5-pro-251215"))
	if facts.OutputTokens != 1280*720*24*5/1024 || facts.Seconds != 5 || !facts.Audio {
		t.Fatalf("estimate: %+v", facts)
	}
	if got := ArkVideoTokens("720p", "16:9", 24, 5); got != 108000 {
		t.Fatalf("token estimate for the documented example should be about 108,900, got %d", got)
	}
}

func TestGeminiStatusURLGuardsOrigin(t *testing.T) {
	ch := Channel{Type: channelTypeGemini, BaseURL: "https://generativelanguage.googleapis.com", Key: "g"}
	driver := geminiDriver{}
	if _, err := driver.BuildStatus(ch, Job{UpstreamID: "https://attacker.example/operations/42"}); err == nil || !errors.Is(err, ErrConfiguration) {
		t.Fatalf("external operation url must be refused as configuration: %v", err)
	}
	status, err := driver.BuildStatus(ch, Job{UpstreamID: "models/veo-3/operations/op-1"})
	if err != nil || status.URL != "https://generativelanguage.googleapis.com/v1beta/models/veo-3/operations/op-1" || status.Headers.Get("x-goog-api-key") != "g" {
		t.Fatalf("status: %+v %v", status, err)
	}
	done, _ := driver.ParseStatus(ch, []byte(`{"done":true,"response":{"generateVideoResponse":{"generatedSamples":[{"video":{"uri":"https://example.com/video.mp4"}}]}}}`))
	if done.State != StateSucceeded || done.ArtifactURL != "https://example.com/video.mp4" {
		t.Fatalf("done: %+v", done)
	}
	pending, _ := driver.ParseStatus(ch, []byte(`{"done":false}`))
	if pending.State != StateRunning {
		t.Fatalf("pending: %+v", pending)
	}
	submit, err := driver.BuildSubmit(ch, dto.VideoRequest{Prompt: "p", Seconds: 8, Size: "720x1280", Quality: "1080p"}, "veo-3.1-generate-preview")
	if err != nil || !strings.HasSuffix(submit.URL, "/models/veo-3.1-generate-preview:predictLongRunning") {
		t.Fatalf("submit: %+v %v", submit, err)
	}
	if !strings.Contains(string(submit.Body), `"aspectRatio":"9:16"`) || !strings.Contains(string(submit.Body), `"resolution":"1080p"`) {
		t.Fatalf("gemini body: %s", submit.Body)
	}
}

func TestEnvelopeLegacyCustomTemplates(t *testing.T) {
	ch := Channel{Type: channelTypeCustom, BaseURL: "https://video.example.com/generate", Key: "k",
		Settings: dto.ChannelOtherSettings{VideoStatusURLTemplate: "/jobs/{id}"}}
	driver := envelopeDriver{}
	submit, err := driver.BuildSubmit(ch, dto.VideoRequest{Prompt: "p", Seconds: 5}, "custom-video")
	if err != nil || submit.URL != "https://video.example.com/generate" {
		t.Fatalf("legacy custom submit goes to the base url verbatim: %+v %v", submit, err)
	}
	status, err := driver.BuildStatus(ch, Job{UpstreamID: "job/42"})
	if err != nil || status.URL != "https://video.example.com/jobs/job%2F42" {
		t.Fatalf("status: %+v %v", status, err)
	}
	if _, err := driver.BuildStatus(Channel{Type: channelTypeCustom, BaseURL: "https://video.example.com/generate"}, Job{UpstreamID: "job"}); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("missing template must be a configuration error: %v", err)
	}
	if _, err := driver.BuildDownload(ch, Job{UpstreamID: "job"}, ""); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("missing content template must be a configuration error: %v", err)
	}
	withContent := ch
	withContent.Settings.VideoContentURLTemplate = "/jobs/{id}/content"
	download, err := driver.BuildDownload(withContent, Job{UpstreamID: "job"}, "")
	if err != nil || !download.Authenticated || download.URL != "https://video.example.com/jobs/job/content" {
		t.Fatalf("content template: %+v %v", download, err)
	}
	relative, err := driver.BuildDownload(Channel{Type: channelTypeCustom, BaseURL: "https://video.example.com/api/v1"}, Job{}, "jobs/123")
	if err != nil || relative.URL != "https://video.example.com/api/v1/jobs/123" {
		t.Fatalf("relative artifact keeps the base path: %+v %v", relative, err)
	}
}

func TestEnvelopeTasksPreset(t *testing.T) {
	ch := Channel{Type: channelTypeOpenAI, BaseURL: "https://aggregator.example", Key: "agg",
		Settings: dto.ChannelOtherSettings{VideoEnvelope: &dto.VideoEnvelopeConfig{Preset: "tasks"}}}
	driver, err := Select(ch, "seedance-2.0")
	if err != nil || driver.Family() != dto.VideoFamilyEnvelope {
		t.Fatalf("select: %v %v", driver, err)
	}
	submit, err := driver.BuildSubmit(ch, dto.VideoRequest{Prompt: "kitten", Seconds: 5, Resolution: "720p", AspectRatio: "16:9",
		FrameImages: []dto.VideoFrameImage{{URL: "https://x/first.png", FrameType: "first_frame"}}}, "seedance-2.0")
	if err != nil || submit.URL != "https://aggregator.example/v1/videos/generations" {
		t.Fatalf("submit: %+v %v", submit, err)
	}
	if !strings.Contains(string(submit.Body), `"duration":5`) || !strings.Contains(string(submit.Body), `"size":"16:9"`) || !strings.Contains(string(submit.Body), `"image_with_roles"`) {
		t.Fatalf("tasks body: %s", submit.Body)
	}
	receipt, err := driver.ParseSubmit(ch, []byte(`{"code":200,"data":[{"status":"submitted","task_id":"task_01KM"}]}`))
	if err != nil || receipt.UpstreamID != "task_01KM" {
		t.Fatalf("array task id: %+v %v", receipt, err)
	}
	status, err := driver.BuildStatus(ch, Job{UpstreamID: "task_01KM"})
	if err != nil || status.URL != "https://aggregator.example/v1/tasks/task_01KM" {
		t.Fatalf("status url: %+v %v", status, err)
	}
	processing, _ := driver.ParseStatus(ch, []byte(`{"code":200,"data":{"status":"processing","progress":40}}`))
	if processing.State != StateRunning {
		t.Fatalf("processing: %+v", processing)
	}
	done, _ := driver.ParseStatus(ch, []byte(`{"code":200,"data":{"status":"completed","cost":0.15,"result":{"videos":[{"url":["https://cdn.aggregator.example/v.mp4"]}]}}}`))
	if done.State != StateSucceeded || done.ArtifactURL != "https://cdn.aggregator.example/v.mp4" || done.Facts == nil || done.Facts.ProviderCostUSD == nil || *done.Facts.ProviderCostUSD != 0.15 || !done.Facts.Reported {
		t.Fatalf("completed: %+v facts=%+v", done, done.Facts)
	}
	failed, _ := driver.ParseStatus(ch, []byte(`{"code":200,"data":{"status":"failed","error":{"code":1001,"message":"nsfw"}}}`))
	if failed.State != StateFailed || failed.ErrorCode != "1001" || failed.ErrorMessage != "nsfw" {
		t.Fatalf("failed: %+v", failed)
	}
	cancelled, _ := driver.ParseStatus(ch, []byte(`{"code":200,"data":{"status":"cancelled"}}`))
	if cancelled.State != StateCancelled {
		t.Fatalf("cancelled: %+v", cancelled)
	}
	download, err := driver.BuildDownload(ch, Job{UpstreamID: "task_01KM"}, "https://cdn.aggregator.example/v.mp4")
	if err != nil || download.Authenticated {
		t.Fatalf("cdn artifact must not carry the key: %+v %v", download, err)
	}
}

func TestCapabilitiesValidate(t *testing.T) {
	caps := DefaultCapabilities("doubao-seedance-1-0-pro-250528")
	if err := caps.Validate(&dto.VideoRequest{Seconds: 5, Size: "1280x720"}); err != nil {
		t.Fatalf("valid request refused: %v", err)
	}
	err := caps.Validate(&dto.VideoRequest{Seconds: 7})
	if err == nil || err.Field != "seconds" || len(err.Allowed) != 2 {
		t.Fatalf("duration outside the list: %v", err)
	}
	err = caps.Validate(&dto.VideoRequest{Seconds: 5, Resolution: "4k"})
	if err == nil || err.Field != "resolution" {
		t.Fatalf("resolution outside the list: %v", err)
	}
	err = caps.Validate(&dto.VideoRequest{Seconds: 5, InputRefs: []dto.VideoInputReference{{URL: "x", Type: "video"}}})
	if err == nil || err.Field != "input" {
		t.Fatalf("reference video unsupported on 1.0: %v", err)
	}
	err = caps.Validate(&dto.VideoRequest{Seconds: 5, Extra: map[string]json.RawMessage{"foo": json.RawMessage(`1`)}})
	if err == nil || err.Field != "foo" {
		t.Fatalf("passthrough outside the allowlist: %v", err)
	}
	if err := caps.Validate(&dto.VideoRequest{Seconds: 5, Extra: map[string]json.RawMessage{"_lazu_media_job_id": json.RawMessage(`"mjob"`), "final_group": json.RawMessage(`"cheap"`), "watermark": json.RawMessage(`false`)}}); err != nil {
		t.Fatalf("the gateway's own fields never fail the passthrough allowlist: %v", err)
	}
	if err := (&dto.VideoCapabilities{}).Validate(&dto.VideoRequest{Seconds: 99, Extra: map[string]json.RawMessage{"seed": json.RawMessage(`42`)}}); err != nil {
		t.Fatalf("empty row constrains nothing: %v", err)
	}
	if DefaultCapabilities("unknown-video").IsEmpty() != true {
		t.Fatal("unknown model gets an empty row")
	}
}

func TestLookupWalksArrays(t *testing.T) {
	payload, _ := decodeObject([]byte(`{"data":[{"task_id":12345}],"nested":{"list":["a","b"]}}`))
	if got := firstString(payload, "data.0.task_id"); got != "12345" {
		t.Fatalf("numeric id rendered: %q", got)
	}
	if got := firstString(payload, "nested.list.1"); got != "b" {
		t.Fatalf("array index: %q", got)
	}
	if got := firstString(payload, "data.3.task_id", "missing"); got != "" {
		t.Fatalf("out of range: %q", got)
	}
}
