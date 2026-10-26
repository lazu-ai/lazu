package gemini

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	openai_chat "github.com/lazu-ai/lazu/internal/protocol/textgen/codec/openai_chat"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/constant"
	"github.com/lazu-ai/lazu/internal/setting/model_setting"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

type Adaptor struct {
}

func (a *Adaptor) ConvertGeminiRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error) {
	if len(request.Contents) > 0 {
		for i, content := range request.Contents {
			if i == 0 {
				if request.Contents[0].Role == "" {
					request.Contents[0].Role = "user"
				}
			}
			for _, part := range content.Parts {
				if part.FileData != nil {
					if part.FileData.MimeType == "" && strings.Contains(part.FileData.FileUri, "www.youtube.com") {
						part.FileData.MimeType = "video/webm"
					}
				}
			}
		}
	}
	return request, nil
}

func (a *Adaptor) ConvertVideoRequest(_ *gin.Context, _ *relaycommon.RelayInfo, request dto.VideoRequest) (any, error) {
	aspectRatio := strings.TrimSpace(request.AspectRatio)
	if aspectRatio == "" {
		aspectRatio = strings.TrimSpace(request.Size)
	}
	switch strings.ToLower(aspectRatio) {
	case "1280x720", "1792x1024", "1536x1024":
		aspectRatio = "16:9"
	case "720x1280", "1024x1792", "1024x1536":
		aspectRatio = "9:16"
	}
	if aspectRatio == "" {
		aspectRatio = "16:9"
	}
	sampleCount := 1
	return dto.GeminiVideoRequest{
		Instances: []dto.GeminiVideoInstance{
			{Prompt: request.Prompt},
		},
		Parameters: dto.GeminiVideoParameters{
			DurationSeconds: request.Seconds,
			AspectRatio:     aspectRatio,
			SampleCount:     sampleCount,
			Resolution:      geminiVideoResolution(request.Quality),
		},
	}, nil
}

func geminiVideoResolution(quality string) string {
	switch strings.ToLower(strings.TrimSpace(quality)) {
	case "high", "hd", "1080p":
		return "1080p"
	case "standard", "medium", "low", "auto", "720p":
		return "720p"
	default:
		return ""
	}
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	if !strings.HasPrefix(info.UpstreamModelName, "imagen") {
		if !model_setting.IsGeminiModelSupportImagine(info.UpstreamModelName) {
			return nil, errors.New("not supported model for image generation")
		}
		parts := []dto.GeminiPart{{Text: request.Prompt}}
		if info.RelayMode == constant.RelayModeImagesEdits {
			// Edits carry reference images: they ride beside the prompt as
			// inline_data parts (image_edits.go).
			references, err := geminiImageEditParts(c)
			if err != nil {
				return nil, err
			}
			parts = append(parts, references...)
		}
		sampleCount := int(request.N)
		if sampleCount <= 0 {
			sampleCount = 1
		}
		geminiRequest := dto.GeminiChatRequest{
			Contents: []dto.GeminiChatContent{
				{
					Role:  "user",
					Parts: parts,
				},
			},
			GenerationConfig: dto.GeminiChatGenerationConfig{
				CandidateCount:     sampleCount,
				ResponseModalities: []string{"TEXT", "IMAGE"},
			},
		}
		return geminiRequest, nil
	}

	// convert size to aspect ratio but allow user to specify aspect ratio
	aspectRatio := "1:1" // default aspect ratio
	size := strings.TrimSpace(request.Size)
	if size != "" {
		if strings.Contains(size, ":") {
			aspectRatio = size
		} else {
			switch size {
			case "256x256", "512x512", "1024x1024":
				aspectRatio = "1:1"
			case "1536x1024":
				aspectRatio = "3:2"
			case "1024x1536":
				aspectRatio = "2:3"
			case "1024x1792":
				aspectRatio = "9:16"
			case "1792x1024":
				aspectRatio = "16:9"
			}
		}
	}

	// build gemini imagen request
	geminiRequest := dto.GeminiImageRequest{
		Instances: []dto.GeminiImageInstance{
			{
				Prompt: request.Prompt,
			},
		},
		Parameters: dto.GeminiImageParameters{
			SampleCount:      int(request.N),
			AspectRatio:      aspectRatio,
			PersonGeneration: "allow_adult", // default allow adult
		},
	}

	// Set imageSize when quality parameter is specified
	// Map quality parameter to imageSize (only supported by Standard and Ultra models)
	// quality values: auto, high, medium, low (for gpt-image-1), hd, standard (for dall-e-3)
	// imageSize values: 1K (default), 2K
	// https://ai.google.dev/gemini-api/docs/imagen
	// https://platform.openai.com/docs/api-reference/images/create
	if request.Quality != "" {
		imageSize := "1K" // default
		switch request.Quality {
		case "hd", "high":
			imageSize = "2K"
		case "2K":
			imageSize = "2K"
		case "standard", "medium", "low", "auto", "1K":
			imageSize = "1K"
		default:
			// unknown quality value, default to 1K
			imageSize = "1K"
		}
		geminiRequest.Parameters.ImageSize = imageSize
	}

	return geminiRequest, nil
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {

}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {

	version := model_setting.GetGeminiVersionSetting(info.UpstreamModelName)

	if info.RelayMode == constant.RelayModeVideoSubmit {
		return fmt.Sprintf("%s/%s/models/%s:predictLongRunning", info.ChannelBaseUrl, version, info.UpstreamModelName), nil
	}

	if strings.HasPrefix(info.UpstreamModelName, "imagen") {
		return fmt.Sprintf("%s/%s/models/%s:predict", info.ChannelBaseUrl, version, info.UpstreamModelName), nil
	}

	if strings.HasPrefix(info.UpstreamModelName, "text-embedding") ||
		strings.HasPrefix(info.UpstreamModelName, "embedding") ||
		strings.HasPrefix(info.UpstreamModelName, "gemini-embedding") {
		action := "embedContent"
		if info.IsGeminiBatchEmbedding {
			action = "batchEmbedContents"
		}
		return fmt.Sprintf("%s/%s/models/%s:%s", info.ChannelBaseUrl, version, info.UpstreamModelName, action), nil
	}

	action := "generateContent"
	if info.IsStream {
		action = "streamGenerateContent?alt=sse"
		if info.RelayMode == constant.RelayModeGemini {
			info.DisablePing = true
		}
	}
	return fmt.Sprintf("%s/%s/models/%s:%s", info.ChannelBaseUrl, version, info.UpstreamModelName, action), nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	if info.RelayMode == constant.RelayModeImagesEdits {
		// The client sent multipart; the converted Gemini request is JSON.
		req.Set("Content-Type", "application/json")
	}
	req.Set("x-goog-api-key", info.ApiKey)
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}

	// A Chat request that reached the channel without a plan is encoded the
	// way a bridged one is: decoded into the IR, then the Gemini codec and the
	// channel's policy (EncodeBridgedRequest).
	body, err := common.Marshal(request)
	if err != nil {
		return nil, err
	}
	semantic, _, err := openai_chat.Decode(body)
	if err != nil {
		return nil, err
	}
	semantic.Model.Upstream = info.UpstreamModelName
	return EncodeBridgedRequest(c, info, semantic)
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	if request.Input == nil {
		return nil, errors.New("input is required")
	}

	inputs := request.ParseInput()
	if len(inputs) == 0 {
		return nil, errors.New("input is empty")
	}
	// We always build a batch-style payload with `requests`, so ensure we call the
	// batch endpoint upstream to avoid payload/endpoint mismatches.
	info.IsGeminiBatchEmbedding = true
	// process all inputs
	geminiRequests := make([]map[string]interface{}, 0, len(inputs))
	for _, input := range inputs {
		geminiRequest := map[string]interface{}{
			"model": fmt.Sprintf("models/%s", info.UpstreamModelName),
			"content": dto.GeminiChatContent{
				Parts: []dto.GeminiPart{
					{
						Text: input,
					},
				},
			},
		}

		// set specific parameters for different models
		// https://ai.google.dev/api/embeddings?hl=zh-cn#method:-models.embedcontent
		switch info.UpstreamModelName {
		case "text-embedding-004", "gemini-embedding-exp-03-07", "gemini-embedding-001":
			// Only newer models introduced after 2024 support OutputDimensionality
			if request.Dimensions > 0 {
				geminiRequest["outputDimensionality"] = request.Dimensions
			}
		}
		geminiRequests = append(geminiRequests, geminiRequest)
	}

	return map[string]interface{}{
		"requests": geminiRequests,
	}, nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.LazuError) {
	if BridgesToGemini(info) {
		if info.IsStream {
			return bridgedStreamHandler(c, resp, info)
		}
		return bridgedHandler(c, resp, info)
	}
	if info.RelayMode == constant.RelayModeGemini {
		if strings.Contains(info.RequestURLPath, ":embedContent") ||
			strings.Contains(info.RequestURLPath, ":batchEmbedContents") {
			return NativeGeminiEmbeddingHandler(c, resp, info)
		}
		if info.IsStream {
			return GeminiTextGenerationStreamHandler(c, info, resp)
		} else {
			return GeminiTextGenerationHandler(c, info, resp)
		}
	}

	if (info.RelayMode == constant.RelayModeImagesGenerations || info.RelayMode == constant.RelayModeImagesEdits) &&
		model_setting.IsGeminiModelSupportImagine(info.UpstreamModelName) {
		return GeminiImageContentHandler(c, info, resp)
	}

	if strings.HasPrefix(info.UpstreamModelName, "imagen") {
		return GeminiImageHandler(c, info, resp)
	}

	// check if the model is an embedding model
	if strings.HasPrefix(info.UpstreamModelName, "text-embedding") ||
		strings.HasPrefix(info.UpstreamModelName, "embedding") ||
		strings.HasPrefix(info.UpstreamModelName, "gemini-embedding") {
		return GeminiEmbeddingHandler(c, info, resp)
	}

	if info.RelayMode == constant.RelayModeChatCompletions && info.ProtocolPlan == nil {
		// an unplanned Chat request (the console playground is not parsed):
		// ConvertOpenAIRequest encoded it through the codec, and its reply is
		// served as a planned Chat client's
		info.ProtocolPlan = &textgen.RelayPlan{
			ClientProtocol:   textgen.Protocol{Family: textgen.ProtocolOpenAIChat, Dialect: "openai"},
			UpstreamProtocol: textgen.Protocol{Family: textgen.ProtocolGemini, Dialect: "gemini"},
		}
		if info.IsStream {
			return bridgedStreamHandler(c, resp, info)
		}
		return bridgedHandler(c, resp, info)
	}
	// text generation for any other client is planned and bridged above
	return nil, types.NewError(fmt.Errorf("a Gemini channel serves text generation through a protocol plan; this request has none"), types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
