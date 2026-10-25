// Package video holds the upstream drivers for asynchronous video generation.
//
// One driver per upstream family covers the whole job lifecycle: how to
// submit, how to read the task id back, how to poll, how to read the state,
// artifact and billing facts, and how to download. The controller and the
// worker call the driver; neither switches on the channel type.
//
// The package is pure: it builds and parses, and never performs I/O, so the
// model package can read its capability defaults without an import cycle.
package video

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/lazu-ai/lazu/internal/dto"
)

// Job states a driver reports. Succeeded carries an artifact; Failed and
// Cancelled are terminal without one; the rest keep polling.
const (
	StateQueued    = "queued"
	StateRunning   = "running"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
)

// ErrConfiguration marks a channel whose settings cannot carry a video job
// (a missing status template, an empty base URL). The worker waits for the
// operator instead of failing the job.
var ErrConfiguration = errors.New("video provider configuration is incomplete")

// ErrUnsupportedInput marks a request the family cannot carry as asked (a
// frame URL scheme the provider does not take). It is the caller's to fix.
var ErrUnsupportedInput = errors.New("video input is not supported by this provider")

// Channel is what a driver needs to know about the upstream.
type Channel struct {
	Type           int
	BaseURL        string
	Key            string
	Settings       dto.ChannelOtherSettings
	HeaderOverride map[string]any
}

// Job is the persisted upstream identity of one generation.
type Job struct {
	UpstreamID  string
	ArtifactURL string
	Model       string
}

type SubmitRequest struct {
	Method  string
	URL     string
	Body    []byte
	Headers http.Header
}

// SubmitReceipt is what the upstream returned for a submission: a task id to
// poll, or, for a synchronous provider, the finished artifact.
type SubmitReceipt struct {
	UpstreamID  string
	ArtifactURL string
}

type StatusRequest struct {
	URL     string
	Headers http.Header
}

// StatusResult is one poll's answer in the gateway's own words.
type StatusResult struct {
	State        string
	ArtifactURL  string
	Facts        *dto.VideoFacts
	ErrorCode    string
	ErrorMessage string
}

type DownloadRequest struct {
	URL string
	// Authenticated downloads carry the channel key and must stay on the
	// provider's origin; unauthenticated ones are external URLs (a CDN) and
	// go through the SSRF filter instead.
	Authenticated bool
	Headers       http.Header
}

// Driver is one upstream family's complete video lifecycle.
type Driver interface {
	Family() string
	// BuildSubmit turns the public request into the upstream request. The
	// caller may still merge extension fields and the channel's parameter
	// override into Body before sending.
	BuildSubmit(ch Channel, req dto.VideoRequest, upstreamModel string) (*SubmitRequest, error)
	ParseSubmit(ch Channel, body []byte) (*SubmitReceipt, error)
	BuildStatus(ch Channel, job Job) (*StatusRequest, error)
	ParseStatus(ch Channel, body []byte) (*StatusResult, error)
	BuildDownload(ch Channel, job Job, artifactURL string) (*DownloadRequest, error)
	// EstimateFacts reads the billing dimensions off the final outbound body,
	// so a channel's parameter override is priced, not the client's wish.
	EstimateFacts(body []byte, caps *dto.VideoCapabilities) dto.VideoFacts
}

// Select picks the driver for a channel and model: an explicit family on the
// channel wins; otherwise the channel type and the model names decide (the
// customer's name and the mapped upstream name are both consulted).
func Select(ch Channel, modelNames ...string) (Driver, error) {
	family := strings.ToLower(strings.TrimSpace(ch.Settings.VideoFamily))
	if family == dto.VideoFamilyAuto {
		family = autoFamily(ch, modelNames...)
	}
	switch family {
	case dto.VideoFamilyGateway:
		return gatewayDriver{}, nil
	case dto.VideoFamilyArk:
		return arkDriver{}, nil
	case dto.VideoFamilyGemini:
		return geminiDriver{}, nil
	case dto.VideoFamilyEnvelope:
		return envelopeDriver{}, nil
	default:
		return nil, errors.New("unknown video family " + family)
	}
}

func autoFamily(ch Channel, modelNames ...string) string {
	ark := false
	for _, name := range modelNames {
		if IsArkVideoModel(name) {
			ark = true
		}
	}
	switch {
	case ch.Type == channelTypeGemini:
		return dto.VideoFamilyGemini
	case ch.Settings.VideoEnvelope != nil:
		// An aggregator serves Seedance through its own envelope.
		return dto.VideoFamilyEnvelope
	case ark && IsArkBaseURL(ch.BaseURL):
		// Seedance elsewhere (an OpenAI-compatible aggregator, a Custom
		// channel) keeps the shape that channel already spoke; an Ark proxy
		// on another host sets video_family: ark.
		return dto.VideoFamilyArk
	case ch.Type == channelTypeCustom:
		return dto.VideoFamilyEnvelope
	default:
		return dto.VideoFamilyGateway
	}
}

// Channel type ids this package needs; they mirror internal/constant without
// importing it, so the package stays importable from the model layer.
const (
	channelTypeOpenAI = 1
	channelTypeCustom = 8
	channelTypeGemini = 24
)

// LegacyFamily is the family a job was submitted through before submissions
// recorded one: the old adaptors chose by channel type only (Gemini type →
// Veo, Custom → the URL templates, anything else → the OpenAI shape).
func LegacyFamily(channelType int) string {
	switch channelType {
	case channelTypeGemini:
		return dto.VideoFamilyGemini
	case channelTypeCustom:
		return dto.VideoFamilyEnvelope
	default:
		return dto.VideoFamilyGateway
	}
}

// IsArkVideoModel reports whether a model name is a Seedance generation
// served by the Volcengine Ark task API (doubao-seedance-*, dreamina-seedance-*).
func IsArkVideoModel(modelName string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(modelName)), "seedance")
}

// arkHostSuffixes are the hosts of the Volcengine Ark and BytePlus ModelArk
// endpoints (ark.cn-beijing.volces.com, ark.ap-southeast.bytepluses.com).
var arkHostSuffixes = []string{"volces.com", "bytepluses.com", "byteplus.com"}

// IsArkBaseURL reports whether a channel's base URL is an Ark endpoint.
func IsArkBaseURL(baseURL string) bool {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL != "" && !strings.Contains(baseURL, "://") {
		baseURL = "https://" + baseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	for _, suffix := range arkHostSuffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}
