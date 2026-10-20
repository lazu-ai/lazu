package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/relay/channel/video"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
)

func TestVideoProviderSubmissionRetrySafety(t *testing.T) {
	if !videoProviderSubmissionRetrySafe(&videoProviderSubmitError{
		StatusCode: http.StatusServiceUnavailable,
		RetrySafe:  true,
		Err:        errors.New("provider rejected request"),
	}) {
		t.Fatal("explicit provider rejection should remain eligible for channel retry")
	}
	if videoProviderSubmissionRetrySafe(&videoProviderSubmitError{
		StatusCode: http.StatusBadGateway,
		Err:        errors.New("response body was truncated"),
	}) {
		t.Fatal("ambiguous post-submission failures must not retry non-idempotent work")
	}
	if videoProviderSubmissionRetrySafe(errors.New("request conversion failed")) {
		t.Fatal("local submission errors must not retry another provider")
	}
	if videoProviderStatusRetrySafe(http.StatusInternalServerError) {
		t.Fatal("ambiguous provider failures must not be retried")
	}
}

func TestSubmitVideoProviderTreatsTransportFailureAsIndeterminate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	// A server that is already closed answers with a transport error: the
	// request may or may not have reached the provider.
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	target := closed.URL + "/v1/videos"
	closed.Close()
	ch := video.Channel{Type: constant.ChannelTypeOpenAI, BaseURL: closed.URL, Key: "k"}
	driver, err := video.Select(ch, "sora-2")
	if err != nil {
		t.Fatalf("select driver: %v", err)
	}
	_, err = submitVideoProvider(context, &preparedVideoProviderRequest{
		Driver:  driver,
		Channel: ch,
		Submit:  &video.SubmitRequest{Method: http.MethodPost, URL: target, Headers: http.Header{}},
		Body:    []byte(`{"model":"sora-2","prompt":"ambiguous transport"}`),
		Info:    &relaycommon.RelayInfo{},
	})
	if err == nil {
		t.Fatal("expected transport error")
	}
	if !videoProviderSubmissionAccepted(err) {
		t.Fatalf("transport failure after dispatch must remain indeterminate: %v", err)
	}
	if videoProviderSubmissionRetrySafe(err) {
		t.Fatalf("indeterminate transport failure must not duplicate provider work: %v", err)
	}
	if videoProviderSubmissionMayHaveBeenDispatched(errors.New("setup request header failed")) {
		t.Fatal("local request setup errors must not be retained as accepted provider work")
	}
}

func TestAlignVideoBillingProbeUsesDurableExtensionRequestID(t *testing.T) {
	probe := &model.MediaJob{
		PublicID:  "probe",
		Source:    model.MediaJobSourceAPI,
		RequestID: "req-inner-relay",
	}
	job := &model.MediaJob{
		PublicID:  "mjob_extension",
		Source:    "test-extension",
		RequestID: "req-outer-extension",
	}
	alignVideoBillingProbeWithJob(probe, job)
	if probe.PublicID != job.PublicID ||
		probe.Source != "test-extension" ||
		probe.RequestID != job.RequestID {
		t.Fatalf("billing probe did not inherit the durable job identity: %+v", probe)
	}
}

func TestCustomVideoSubmissionRequiresPollingTemplate(t *testing.T) {
	ch := video.Channel{Type: constant.ChannelTypeCustom, BaseURL: "https://video.example.com/generate", Key: "k"}
	driver, err := video.Select(ch, "custom-video")
	if err != nil {
		t.Fatalf("select driver: %v", err)
	}
	if err := validateVideoProviderPollingConfiguration(driver, ch); err == nil {
		t.Fatal("custom video submission without a status template should be rejected before dispatch")
	}
	ch.Settings = dto.ChannelOtherSettings{
		VideoStatusURLTemplate: "/jobs/{id}",
	}
	if err := validateVideoProviderPollingConfiguration(driver, ch); err != nil {
		t.Fatalf("configured custom video submission should be allowed: %v", err)
	}
}

func TestMergeVideoProviderExtraFieldsPreservesSafeExtensions(t *testing.T) {
	body, err := mergeVideoProviderExtraFields(
		[]byte(`{"model":"sora-2","prompt":"canonical"}`),
		map[string]json.RawMessage{
			"seed":                         json.RawMessage(`42`),
			"reference_media":              json.RawMessage(`{"url":"https://example.com/reference.png"}`),
			"prompt":                       json.RawMessage(`"must not replace"`),
			precreatedMediaJobIDRequestKey: json.RawMessage(`"mjob_internal"`),
			"final_group":                  json.RawMessage(`"cheap"`),
		},
	)
	if err != nil {
		t.Fatalf("mergeVideoProviderExtraFields error: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("decode merged provider request: %v", err)
	}
	if fields["seed"] != float64(42) || fields["reference_media"] == nil {
		t.Fatalf("safe provider extensions were lost: %+v", fields)
	}
	if fields["prompt"] != "canonical" {
		t.Fatalf("extension field replaced canonical prompt: %+v", fields)
	}
	if _, exists := fields[precreatedMediaJobIDRequestKey]; exists {
		t.Fatalf("internal media job field leaked upstream: %+v", fields)
	}
	if _, exists := fields["final_group"]; exists {
		t.Fatalf("routing metadata leaked upstream: %+v", fields)
	}
}
