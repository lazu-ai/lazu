package dto

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/types"
)

// Video frame roles and reference kinds on the public contract. They follow
// OpenRouter's extension of the OpenAI video object so a client written for
// one gateway works against the other.
const (
	VideoFrameFirst = "first_frame"
	VideoFrameLast  = "last_frame"

	VideoReferenceImage = "image"
	VideoReferenceVideo = "video"
	VideoReferenceAudio = "audio"

	VideoInputText           = "text"
	VideoInputFirstFrame     = "first_frame"
	VideoInputLastFrame      = "last_frame"
	VideoInputReferenceImage = "reference_image"
	VideoInputReferenceVideo = "reference_video"
	VideoInputReferenceAudio = "reference_audio"
)

type VideoFrameImage struct {
	URL       string `json:"url"`
	FrameType string `json:"frame_type"`
}

type VideoInputReference struct {
	URL  string `json:"url"`
	Type string `json:"type,omitempty"`
}

type VideoRequest struct {
	Model   string `json:"model"`
	Prompt  string `json:"prompt" binding:"required"`
	Seconds int    `json:"seconds,omitempty"`
	// Size is either WIDTHxHEIGHT or an aspect ratio; AspectRatio and
	// Resolution are the explicit forms. EffectiveResolution and EffectiveRatio
	// reconcile the three.
	Size           string                `json:"size,omitempty"`
	AspectRatio    string                `json:"aspect_ratio,omitempty"`
	Resolution     string                `json:"resolution,omitempty"`
	Quality        string                `json:"quality,omitempty"`
	ResponseFormat string                `json:"response_format,omitempty"`
	User           json.RawMessage       `json:"user,omitempty"`
	FrameImages    []VideoFrameImage     `json:"frame_images,omitempty"`
	InputRefs      []VideoInputReference `json:"input_references,omitempty"`
	GenerateAudio  *bool                 `json:"generate_audio,omitempty"`
	Seed           *int64                `json:"seed,omitempty"`
	Provider       string                `json:"provider,omitempty"`
	ArtifactFileID string                `json:"artifact_file_id,omitempty"`
	ArtifactURL    string                `json:"artifact_url,omitempty"`
	UpstreamJobID  string                `json:"upstream_job_id,omitempty"`
	// WebhookURL is accepted so it never leaks upstream as an unknown field;
	// Lazu does not deliver video webhooks.
	WebhookURL string                     `json:"webhook_url,omitempty"`
	Extra      map[string]json.RawMessage `json:"-"`
}

func (r *VideoRequest) UnmarshalJSON(data []byte) error {
	var rawMap map[string]json.RawMessage
	if err := common.Unmarshal(data, &rawMap); err != nil {
		return err
	}
	knownFields := GetJSONFieldNames(reflect.TypeOf(*r))
	type Alias VideoRequest
	var known Alias
	if err := common.Unmarshal(data, &known); err != nil {
		return err
	}
	*r = VideoRequest(known)
	r.Extra = make(map[string]json.RawMessage)
	for key, value := range rawMap {
		if _, ok := knownFields[key]; !ok {
			r.Extra[key] = value
		}
	}
	return nil
}

func (r *VideoRequest) GetTokenCountMeta() *types.TokenCountMeta {
	return &types.TokenCountMeta{
		CombineText: r.Prompt,
	}
}

func (r *VideoRequest) IsStream(c *gin.Context) bool {
	return false
}

func (r *VideoRequest) SetModelName(modelName string) {
	if modelName != "" {
		r.Model = modelName
	}
}

// EffectiveResolution returns the requested resolution tier ("480p", "720p",
// "1080p", "4k") from the explicit field, the quality shorthand some clients send,
// or a WIDTHxHEIGHT size. Empty when nothing says.
func (r VideoRequest) EffectiveResolution() string {
	for _, candidate := range []string{r.Resolution, r.Quality, r.Size} {
		if tier := NormalizeVideoResolution(candidate); tier != "" {
			return tier
		}
	}
	return ""
}

// EffectiveRatio returns the requested aspect ratio ("16:9") from the explicit
// field or a size written as a ratio or as WIDTHxHEIGHT. Empty when nothing says.
func (r VideoRequest) EffectiveRatio() string {
	for _, candidate := range []string{r.AspectRatio, r.Size} {
		if ratio := NormalizeVideoRatio(candidate); ratio != "" {
			return ratio
		}
	}
	return ""
}

// InputKinds lists the capability inputs the request uses, for validation.
func (r VideoRequest) InputKinds() []string {
	kinds := []string{VideoInputText}
	for _, frame := range r.FrameImages {
		switch strings.ToLower(strings.TrimSpace(frame.FrameType)) {
		case VideoFrameLast:
			kinds = append(kinds, VideoInputLastFrame)
		case VideoFrameFirst:
			kinds = append(kinds, VideoInputFirstFrame)
		}
	}
	for _, reference := range r.InputRefs {
		switch strings.ToLower(strings.TrimSpace(reference.Type)) {
		case VideoReferenceVideo:
			kinds = append(kinds, VideoInputReferenceVideo)
		case VideoReferenceAudio:
			kinds = append(kinds, VideoInputReferenceAudio)
		default:
			kinds = append(kinds, VideoInputReferenceImage)
		}
	}
	return kinds
}

// NormalizeVideoResolution maps "720p", "1080P", "hd", "4K" or "1280x720" to a
// tier name. Unknown words return "".
func NormalizeVideoResolution(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "":
		return ""
	case "480p", "sd":
		return "480p"
	case "720p", "hd":
		return "720p"
	case "1080p", "fhd", "full_hd":
		return "1080p"
	case "2k", "1440p":
		return "2k"
	case "4k", "2160p", "uhd":
		return "4k"
	}
	width, height, ok := parseVideoSize(value)
	if !ok {
		return ""
	}
	short := width
	if height < short {
		short = height
	}
	switch {
	case short >= 2000:
		return "4k"
	case short >= 1300:
		return "2k"
	case short >= 1000:
		return "1080p"
	case short >= 700:
		return "720p"
	default:
		return "480p"
	}
}

// NormalizeVideoRatio maps "16:9", "16/9" or "1280x720" to "W:H" in lowest
// terms, and keeps provider words such as "adaptive". Unknown values return "".
func NormalizeVideoRatio(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return ""
	}
	if value == "adaptive" || value == "auto" {
		return "adaptive"
	}
	separator := ":"
	if strings.Contains(value, "/") && !strings.Contains(value, ":") {
		separator = "/"
	}
	if parts := strings.Split(value, separator); len(parts) == 2 {
		width, errW := strconv.Atoi(strings.TrimSpace(parts[0]))
		height, errH := strconv.Atoi(strings.TrimSpace(parts[1]))
		if errW == nil && errH == nil && width > 0 && height > 0 {
			return ratioString(width, height)
		}
	}
	if width, height, ok := parseVideoSize(value); ok {
		return ratioString(width, height)
	}
	return ""
}

// VideoSizeForResolution returns a WIDTHxHEIGHT for a tier and ratio, used
// when a provider wants pixels and for token estimates. Unknown tiers return 0.
func VideoSizeForResolution(tier string, ratio string) (int, int) {
	short := 0
	switch NormalizeVideoResolution(tier) {
	case "480p":
		short = 480
	case "720p":
		short = 720
	case "1080p":
		short = 1080
	case "2k":
		short = 1440
	case "4k":
		short = 2160
	default:
		return 0, 0
	}
	ratioW, ratioH := 16, 9
	if parts := strings.Split(NormalizeVideoRatio(ratio), ":"); len(parts) == 2 {
		if w, err := strconv.Atoi(parts[0]); err == nil && w > 0 {
			if h, err := strconv.Atoi(parts[1]); err == nil && h > 0 {
				ratioW, ratioH = w, h
			}
		}
	}
	if ratioW >= ratioH {
		return short * ratioW / ratioH, short
	}
	return short, short * ratioH / ratioW
}

func parseVideoSize(value string) (int, int, bool) {
	parts := strings.Split(strings.ToLower(value), "x")
	if len(parts) != 2 {
		return 0, 0, false
	}
	width, errW := strconv.Atoi(strings.TrimSpace(parts[0]))
	height, errH := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errW != nil || errH != nil || width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

func ratioString(width int, height int) string {
	gcd := func(a, b int) int {
		for b != 0 {
			a, b = b, a%b
		}
		return a
	}
	divisor := gcd(width, height)
	return fmt.Sprintf("%d:%d", width/divisor, height/divisor)
}

// VideoFacts are what a provider reported, or what the gateway estimated,
// about one generation: the billing dimensions of a video job. Reported is
// true when the values came from the provider's terminal response.
type VideoFacts struct {
	Seconds         int64    `json:"seconds,omitempty"`
	Resolution      string   `json:"resolution,omitempty"`
	Ratio           string   `json:"ratio,omitempty"`
	FPS             int      `json:"fps,omitempty"`
	OutputTokens    int64    `json:"output_tokens,omitempty"`
	ProviderCostUSD *float64 `json:"provider_cost_usd,omitempty"`
	Audio           bool     `json:"audio,omitempty"`
	Reported        bool     `json:"reported,omitempty"`
}

// IsZero reports whether no dimension is known.
func (f VideoFacts) IsZero() bool {
	return f.Seconds <= 0 && f.Resolution == "" && f.OutputTokens <= 0 && f.ProviderCostUSD == nil
}

// MediaJobUsage is the public face of VideoFacts on a job object: procurement
// cost is never exposed.
type MediaJobUsage struct {
	Seconds      int64  `json:"seconds,omitempty"`
	Resolution   string `json:"resolution,omitempty"`
	Ratio        string `json:"ratio,omitempty"`
	FPS          int    `json:"fps,omitempty"`
	OutputTokens int64  `json:"output_tokens,omitempty"`
	Audio        bool   `json:"audio,omitempty"`
}

func (f VideoFacts) Public() *MediaJobUsage {
	if f.IsZero() {
		return nil
	}
	return &MediaJobUsage{
		Seconds:      f.Seconds,
		Resolution:   f.Resolution,
		Ratio:        f.Ratio,
		FPS:          f.FPS,
		OutputTokens: f.OutputTokens,
		Audio:        f.Audio,
	}
}

// VideoCapabilities is one model's row of what it can take: the same data
// drives request validation, the catalog's parameter table, client option
// pickers and price estimates. Every list is optional; an empty list means
// "no restriction known", never "nothing allowed".
type VideoCapabilities struct {
	Durations         []int    `json:"durations,omitempty"`
	MinSeconds        int      `json:"min_seconds,omitempty"`
	MaxSeconds        int      `json:"max_seconds,omitempty"`
	Resolutions       []string `json:"resolutions,omitempty"`
	Ratios            []string `json:"ratios,omitempty"`
	Inputs            []string `json:"inputs,omitempty"`
	MaxReferences     int      `json:"max_references,omitempty"`
	Audio             *bool    `json:"audio,omitempty"`
	Passthrough       []string `json:"passthrough,omitempty"`
	DefaultSeconds    int      `json:"default_seconds,omitempty"`
	DefaultResolution string   `json:"default_resolution,omitempty"`
	DefaultRatio      string   `json:"default_ratio,omitempty"`
	FPS               int      `json:"fps,omitempty"`
}

// IsEmpty reports whether the row says nothing: no restriction and no
// default. A row with only defaults is kept, since the catalog and the
// estimate read them.
func (c *VideoCapabilities) IsEmpty() bool {
	return c == nil || (len(c.Durations) == 0 && c.MinSeconds == 0 && c.MaxSeconds == 0 &&
		len(c.Resolutions) == 0 && len(c.Ratios) == 0 && len(c.Inputs) == 0 &&
		c.MaxReferences == 0 && c.Audio == nil && len(c.Passthrough) == 0 &&
		c.DefaultSeconds == 0 && c.DefaultResolution == "" && c.DefaultRatio == "" && c.FPS == 0)
}

// ValidateInputs checks the request's own vocabulary, whatever the model:
// every frame names first_frame or last_frame, and every reference names
// image, video or audio (or nothing, meaning image). A value outside these
// would otherwise be read as something the caller did not ask for.
func (r *VideoRequest) ValidateInputs() *VideoCapabilityError {
	if r == nil {
		return nil
	}
	for _, frame := range r.FrameImages {
		switch strings.ToLower(strings.TrimSpace(frame.FrameType)) {
		case VideoFrameFirst, VideoFrameLast:
		default:
			return &VideoCapabilityError{Field: "frame_images.frame_type", Value: frame.FrameType, Allowed: []string{VideoFrameFirst, VideoFrameLast}}
		}
		if strings.TrimSpace(frame.URL) == "" {
			return &VideoCapabilityError{Field: "frame_images.url", Value: "", Allowed: []string{"an image URL"}}
		}
	}
	for _, reference := range r.InputRefs {
		switch strings.ToLower(strings.TrimSpace(reference.Type)) {
		case "", VideoReferenceImage, VideoReferenceVideo, VideoReferenceAudio:
		default:
			return &VideoCapabilityError{Field: "input_references.type", Value: reference.Type, Allowed: []string{VideoReferenceImage, VideoReferenceVideo, VideoReferenceAudio}}
		}
		if strings.TrimSpace(reference.URL) == "" {
			return &VideoCapabilityError{Field: "input_references.url", Value: "", Allowed: []string{"a media URL"}}
		}
	}
	return nil
}

// VideoCapabilityError names the field that fell outside the model's
// capabilities and the values it accepts, for a 400 that tells the caller
// what to send instead.
type VideoCapabilityError struct {
	Field   string
	Value   string
	Allowed []string
}

func (e *VideoCapabilityError) Error() string {
	if len(e.Allowed) == 0 {
		return fmt.Sprintf("%s %q is not supported by this model", e.Field, e.Value)
	}
	return fmt.Sprintf("%s %q is not supported by this model; allowed: %s", e.Field, e.Value, strings.Join(e.Allowed, ", "))
}

// Validate checks a request against the row. A nil or empty row accepts
// everything. Passthrough is enforced only when the row lists it.
func (c *VideoCapabilities) Validate(req *VideoRequest) *VideoCapabilityError {
	if c.IsEmpty() || req == nil {
		return nil
	}
	if req.Seconds > 0 {
		if len(c.Durations) > 0 && !containsInt(c.Durations, req.Seconds) {
			return &VideoCapabilityError{Field: "seconds", Value: strconv.Itoa(req.Seconds), Allowed: intStrings(c.Durations)}
		}
		if c.MinSeconds > 0 && req.Seconds < c.MinSeconds || c.MaxSeconds > 0 && req.Seconds > c.MaxSeconds {
			return &VideoCapabilityError{Field: "seconds", Value: strconv.Itoa(req.Seconds), Allowed: []string{fmt.Sprintf("%d-%d", c.MinSeconds, c.MaxSeconds)}}
		}
	}
	if len(c.Resolutions) > 0 {
		// An explicit resolution the gateway cannot read is refused rather
		// than dropped: the provider would pick its default while the quote
		// assumed another.
		if raw := strings.TrimSpace(req.Resolution); raw != "" && NormalizeVideoResolution(raw) == "" {
			return &VideoCapabilityError{Field: "resolution", Value: raw, Allowed: c.Resolutions}
		}
		if resolution := req.EffectiveResolution(); resolution != "" && !containsFold(c.Resolutions, resolution) {
			return &VideoCapabilityError{Field: "resolution", Value: resolution, Allowed: c.Resolutions}
		}
	}
	if ratio := req.EffectiveRatio(); ratio != "" && len(c.Ratios) > 0 && !containsFold(c.Ratios, ratio) {
		return &VideoCapabilityError{Field: "aspect_ratio", Value: ratio, Allowed: c.Ratios}
	}
	if len(c.Inputs) > 0 {
		for _, kind := range req.InputKinds() {
			if kind == VideoInputText {
				continue
			}
			if !containsFold(c.Inputs, kind) {
				return &VideoCapabilityError{Field: "input", Value: kind, Allowed: c.Inputs}
			}
		}
	}
	if c.MaxReferences > 0 && len(req.InputRefs) > c.MaxReferences {
		return &VideoCapabilityError{Field: "input_references", Value: strconv.Itoa(len(req.InputRefs)), Allowed: []string{fmt.Sprintf("at most %d", c.MaxReferences)}}
	}
	if c.Audio != nil && !*c.Audio && req.GenerateAudio != nil && *req.GenerateAudio {
		return &VideoCapabilityError{Field: "generate_audio", Value: "true", Allowed: []string{"false"}}
	}
	if len(c.Passthrough) > 0 {
		keys := make([]string, 0, len(req.Extra))
		for key := range req.Extra {
			if IsInternalVideoRequestField(key) {
				continue
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if !containsFold(c.Passthrough, key) {
				return &VideoCapabilityError{Field: key, Value: "", Allowed: c.Passthrough}
			}
		}
	}
	return nil
}

// IsInternalVideoRequestField reports a body key the gateway itself adds
// (a pre-created job id, routing metadata): never a provider parameter, so neither
// validated against a model's passthrough list nor sent upstream.
func IsInternalVideoRequestField(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" || strings.HasPrefix(key, "_") {
		return true
	}
	switch strings.ToLower(key) {
	case "channel_group", "channel_id", "channel_multi_key_index", "final_group", "original_model":
		return true
	default:
		return false
	}
}

func containsInt(values []int, wanted int) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func containsFold(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(wanted)) {
			return true
		}
	}
	return false
}

func intStrings(values []int) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, strconv.Itoa(value))
	}
	return out
}

// Video upstream families. A channel's family decides which driver builds
// its requests and reads its answers; see internal/relay/channel/video.
const (
	VideoFamilyAuto     = ""
	VideoFamilyGateway  = "gateway"
	VideoFamilyArk      = "ark"
	VideoFamilyGemini   = "gemini"
	VideoFamilyEnvelope = "envelope"
)

// VideoEnvelopeConfig describes an aggregator whose every video model shares
// one task envelope: where to submit and poll, and the JSON paths that carry
// the task id, state, artifact and cost. URLs may be absolute or relative to
// the channel base URL; {id} is the upstream task id. A Preset fills the
// request body shape and default paths; fields set here win over it.
type VideoEnvelopeConfig struct {
	Preset           string            `json:"preset,omitempty"`
	SubmitURL        string            `json:"submit_url,omitempty"`
	StatusURL        string            `json:"status_url,omitempty"`
	DownloadURL      string            `json:"download_url,omitempty"`
	JobIDPaths       []string          `json:"job_id_paths,omitempty"`
	StatusPath       string            `json:"status_path,omitempty"`
	StatusMap        map[string]string `json:"status_map,omitempty"`
	ArtifactPaths    []string          `json:"artifact_paths,omitempty"`
	ErrorCodePath    string            `json:"error_code_path,omitempty"`
	ErrorMessagePath string            `json:"error_message_path,omitempty"`
	CostPath         string            `json:"cost_path,omitempty"`
	SecondsPath      string            `json:"seconds_path,omitempty"`
	ResolutionPath   string            `json:"resolution_path,omitempty"`
	AuthHeader       string            `json:"auth_header,omitempty"`
}

// Envelope presets by name.
const (
	VideoEnvelopePresetOpenAI = "openai"
	VideoEnvelopePresetTasks  = "tasks"
)

type OpenAIVideoRequest struct {
	Model          string                `json:"model"`
	Prompt         string                `json:"prompt"`
	Seconds        int                   `json:"seconds,omitempty"`
	Size           string                `json:"size,omitempty"`
	AspectRatio    string                `json:"aspect_ratio,omitempty"`
	Resolution     string                `json:"resolution,omitempty"`
	Quality        string                `json:"quality,omitempty"`
	ResponseFormat string                `json:"response_format,omitempty"`
	User           json.RawMessage       `json:"user,omitempty"`
	FrameImages    []VideoFrameImage     `json:"frame_images,omitempty"`
	InputRefs      []VideoInputReference `json:"input_references,omitempty"`
	GenerateAudio  *bool                 `json:"generate_audio,omitempty"`
	Seed           *int64                `json:"seed,omitempty"`
}

type GeminiVideoRequest struct {
	Instances  []GeminiVideoInstance `json:"instances"`
	Parameters GeminiVideoParameters `json:"parameters,omitempty"`
}

type GeminiVideoInstance struct {
	Prompt    string            `json:"prompt"`
	Image     *GeminiVideoImage `json:"image,omitempty"`
	LastFrame *GeminiVideoImage `json:"lastFrame,omitempty"`
}

type GeminiVideoImage struct {
	GcsURI             string `json:"gcsUri,omitempty"`
	BytesBase64Encoded string `json:"bytesBase64Encoded,omitempty"`
	MimeType           string `json:"mimeType,omitempty"`
}

type GeminiVideoParameters struct {
	DurationSeconds int    `json:"durationSeconds,omitempty"`
	AspectRatio     string `json:"aspectRatio,omitempty"`
	SampleCount     int    `json:"sampleCount,omitempty"`
	Resolution      string `json:"resolution,omitempty"`
	GenerateAudio   *bool  `json:"generateAudio,omitempty"`
	Seed            *int64 `json:"seed,omitempty"`
}
