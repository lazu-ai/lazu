package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	"github.com/lazu-ai/lazu/internal/relay/channel"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/common_handler"
	relayconstant "github.com/lazu-ai/lazu/internal/relay/constant"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/packages/domain/providercap"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type Adaptor struct {
	ChannelType    int
	ResponseFormat string
}

func (a *Adaptor) ConvertGeminiRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error) {
	if request.HasCurrentTurnThoughtSignature() {
		return nil, errors.New("Gemini thoughtSignature after the latest user turn requires a Gemini-protocol upstream; OpenAI-compatible upstreams cannot faithfully relay native Gemini tool-call history")
	}

	// 使用 service.GeminiToOpenAIRequest 转换请求格式
	openaiRequest, err := service.GeminiToOpenAIRequest(request, info)
	if err != nil {
		return nil, err
	}
	return a.ConvertOpenAIRequest(c, info, openaiRequest)
}

func (a *Adaptor) ConvertVideoRequest(_ *gin.Context, _ *relaycommon.RelayInfo, request dto.VideoRequest) (any, error) {
	return dto.OpenAIVideoRequest{
		Model:          request.Model,
		Prompt:         request.Prompt,
		Seconds:        request.Seconds,
		Size:           request.Size,
		AspectRatio:    request.AspectRatio,
		Quality:        request.Quality,
		ResponseFormat: request.ResponseFormat,
		User:           request.User,
	}, nil
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	//if !strings.Contains(request.Model, "claude") {
	//	return nil, fmt.Errorf("you are using openai channel type with path /v1/messages, only claude model supported convert, but got %s", request.Model)
	//}
	//if common.DebugEnabled {
	//	bodyBytes := []byte(common.GetJsonString(request))
	//	err := os.WriteFile(fmt.Sprintf("claude_request_%s.txt", c.GetString(common.RequestIdKey)), bodyBytes, 0644)
	//	if err != nil {
	//		println(fmt.Sprintf("failed to save request body to file: %v", err))
	//	}
	//}
	aiRequest, err := service.ClaudeToOpenAIRequest(*request, info)
	if err != nil {
		return nil, err
	}
	//if common.DebugEnabled {
	//	println(fmt.Sprintf("convert claude to openai request result: %s", common.GetJsonString(aiRequest)))
	//	// Save request body to file for debugging
	//	bodyBytes := []byte(common.GetJsonString(aiRequest))
	//	err = os.WriteFile(fmt.Sprintf("claude_to_openai_request_%s.txt", c.GetString(common.RequestIdKey)), bodyBytes, 0644)
	//	if err != nil {
	//		println(fmt.Sprintf("failed to save request body to file: %v", err))
	//	}
	//}
	if info.SupportStreamOptions && info.IsStream {
		aiRequest.StreamOptions = &dto.StreamOptions{
			IncludeUsage: true,
		}
	}
	return a.ConvertOpenAIRequest(c, info, aiRequest)
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType

	// initialize ThinkingContentInfo when thinking_to_content is enabled
	if info.ChannelSetting.ThinkingToContent {
		info.ThinkingContentInfo = relaycommon.ThinkingContentInfo{
			IsFirstThinkingContent:  true,
			SendLastThinkingContent: false,
			HasSentThinkingContent:  false,
		}
	}
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info.RelayMode == relayconstant.RelayModeRealtime {
		if strings.HasPrefix(info.ChannelBaseUrl, "https://") {
			baseUrl := strings.TrimPrefix(info.ChannelBaseUrl, "https://")
			baseUrl = "wss://" + baseUrl
			info.ChannelBaseUrl = baseUrl
		} else if strings.HasPrefix(info.ChannelBaseUrl, "http://") {
			baseUrl := strings.TrimPrefix(info.ChannelBaseUrl, "http://")
			baseUrl = "ws://" + baseUrl
			info.ChannelBaseUrl = baseUrl
		}
	}
	switch info.ChannelType {
	case constant.ChannelTypeCustom:
		url := info.ChannelBaseUrl
		url = strings.Replace(url, "{model}", info.UpstreamModelName, -1)
		return url, nil
	default:
		// a planned attempt goes to the endpoint its plan chose, a bridged
		// Claude client included; unplanned Claude and Gemini clients are
		// converted to Chat by the legacy path
		if (info.RelayFormat == types.RelayFormatClaude || info.RelayFormat == types.RelayFormatGemini) && info.ProtocolPlan == nil {
			return fmt.Sprintf("%s/v1/chat/completions", info.ChannelBaseUrl), nil
		}
		requestPath := info.RequestURLPath
		if info.ProtocolPlan != nil {
			switch info.ProtocolPlan.UpstreamProtocol.Family {
			case textgen.ProtocolOpenAIResponses:
				if info.ProtocolPlan.UpstreamProtocol.Dialect == providercap.DialectAzureOpenAI {
					return azureResponsesRequestURL(info.ChannelBaseUrl, info.ChannelOtherSettings.AzureResponsesVersion)
				}
				requestPath = "/v1/responses"
			case textgen.ProtocolOpenAIChat:
				if info.ProtocolPlan.UpstreamProtocol.Dialect == providercap.DialectAzureOpenAI {
					return azureChatRequestURL(info.ChannelBaseUrl, info.ChannelOtherSettings.AzureResponsesVersion, info.UpstreamModelName)
				}
				requestPath = "/v1/chat/completions"
			}
		}
		return relaycommon.GetFullRequestURL(info.ChannelBaseUrl, requestPath, info.ChannelType), nil
	}
}

func azureResponsesRequestURL(baseURL, configuredVersion string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid Azure OpenAI base URL %q", baseURL)
	}
	version := strings.TrimSpace(configuredVersion)
	if version == "" {
		version = "preview"
	}
	route := "/openai/responses"
	if strings.EqualFold(version, "preview") || strings.EqualFold(version, "latest") {
		route = "/openai/v1/responses"
	}
	parsed.Path = azurePathPrefix(parsed.Path) + route
	parsed.RawPath = ""
	query := parsed.Query()
	query.Set("api-version", version)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func azureChatRequestURL(baseURL, configuredVersion, deployment string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid Azure OpenAI base URL %q", baseURL)
	}
	version := strings.TrimSpace(configuredVersion)
	route := "/openai/v1/chat/completions"
	query := parsed.Query()
	if version != "" && !strings.EqualFold(version, "preview") && !strings.EqualFold(version, "latest") {
		deployment = strings.TrimSpace(deployment)
		if deployment == "" {
			return "", fmt.Errorf("Azure OpenAI deployment name is required for API version %q", version)
		}
		route = "/openai/deployments/" + deployment + "/chat/completions"
		query.Set("api-version", version)
	} else {
		query.Del("api-version")
	}
	parsed.Path = azurePathPrefix(parsed.Path) + route
	parsed.RawPath = ""
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func azurePathPrefix(value string) string {
	path := strings.TrimRight(value, "/")
	lowerPath := strings.ToLower(path)
	if index := strings.Index(lowerPath, "/openai"); index >= 0 {
		boundary := index + len("/openai")
		if boundary == len(path) || path[boundary] == '/' {
			path = path[:index]
		}
	}
	return strings.TrimRight(path, "/")
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, header *http.Header, info *relaycommon.RelayInfo) error {
	if info.ChannelMeta != nil && info.ChannelOtherSettings.CodexClientOnly && !common.IsCodexResponsesRequest(c.Request) {
		return errors.New("channel requires an original Codex Responses request")
	}
	channel.SetupApiRequestHeader(info, c, header)
	if info.ChannelType == constant.ChannelTypeOpenAI && "" != info.Organization {
		header.Set("OpenAI-Organization", info.Organization)
	}
	if info.RelayMode == relayconstant.RelayModeRealtime {
		swp := c.Request.Header.Get("Sec-WebSocket-Protocol")
		if swp != "" {
			items := []string{
				"realtime",
				"openai-insecure-api-key." + info.ApiKey,
				"openai-beta.realtime-v1",
			}
			header.Set("Sec-WebSocket-Protocol", strings.Join(items, ","))
			//req.Header.Set("Sec-WebSocket-Key", c.Request.Header.Get("Sec-WebSocket-Key"))
			//req.Header.Set("Sec-Websocket-Extensions", c.Request.Header.Get("Sec-Websocket-Extensions"))
			//req.Header.Set("Sec-Websocket-Version", c.Request.Header.Get("Sec-Websocket-Version"))
		} else {
			header.Set("openai-beta", "realtime=v1")
			header.Set("Authorization", "Bearer "+info.ApiKey)
		}
	} else if info.ProtocolPlan != nil && info.ProtocolPlan.UpstreamProtocol.Dialect == providercap.DialectAzureOpenAI {
		// Azure accepts either an api-key credential or an explicitly configured
		// Entra bearer token. Header overrides are applied before this adaptor, so
		// preserve either explicit form and synthesize api-key only when absent.
		if header.Get("Authorization") == "" && header.Get("api-key") == "" {
			header.Set("api-key", info.ApiKey)
		}
	} else {
		header.Set("Authorization", "Bearer "+info.ApiKey)
	}
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	if info.ChannelType != constant.ChannelTypeOpenAI {
		request.StreamOptions = nil
	}
	// every sampling field left out of the request, said once to the client
	// at the end (relaycommon.WarnSamplingRefused)
	var dropped []string
	if strings.HasPrefix(info.UpstreamModelName, "o") || strings.HasPrefix(info.UpstreamModelName, "gpt-5") {
		if request.MaxCompletionTokens == 0 && request.MaxTokens != 0 {
			request.MaxCompletionTokens = request.MaxTokens
			request.MaxTokens = 0
		}

		// the o series and gpt-5 (but its chat model) take no temperature
		if strings.HasPrefix(info.UpstreamModelName, "o") || info.UpstreamModelName != "gpt-5-chat-latest" {
			if request.Temperature != nil {
				dropped = append(dropped, "temperature")
			}
			request.Temperature = nil
		}

		info.ReasoningEffort = request.ReasoningEffort

		// o系列模型developer适配（o1-mini除外）
		if !strings.HasPrefix(info.UpstreamModelName, "o1-mini") && !strings.HasPrefix(info.UpstreamModelName, "o1-preview") {
			//修改第一个Message的内容，将system改为developer
			if len(request.Messages) > 0 && request.Messages[0].Role == "system" {
				request.Messages[0].Role = "developer"
			}
		}
	}

	if chatToolsNeedReasoningOff(info, request) {
		request.ReasoningEffort = "none"
	}

	if request.Temperature != nil || request.TopP != nil {
		effort := request.ReasoningEffort
		if effort == "" && len(request.Reasoning) > 0 {
			effort = gjson.GetBytes(request.Reasoning, "effort").Str
		}
		dropped = append(dropped, dropSampling(samplingRefusedWhileReasoning(info, effort), &request.Temperature, &request.TopP)...)
	}
	relaycommon.WarnSamplingRefused(c, info, dropped)

	return request, nil
}

// dropSampling clears the refused fields of a typed request and names the
// ones the request carried, for the client's warning.
func dropSampling(refused []string, temperature, topP **float64) []string {
	var dropped []string
	for _, field := range refused {
		switch field {
		case "temperature":
			if *temperature != nil {
				dropped = append(dropped, field)
			}
			*temperature = nil
		case "top_p":
			if *topP != nil {
				dropped = append(dropped, field)
			}
			*topP = nil
		}
	}
	return dropped
}

// chatToolsNeedReasoningOff mirrors the semantic attempt's rule on the native
// Chat wire: a model that takes tools on Chat only with reasoning off gets
// reasoning_effort "none" when the client sent tools and no effort of its own.
// Only an official OpenAI surface is touched; what a chat-compatible reseller
// does with the field is its own business.
func chatToolsNeedReasoningOff(info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) bool {
	if info == nil || info.ProtocolPlan == nil || !info.ProtocolPlan.Native ||
		info.ProtocolPlan.UpstreamProtocol.Dialect != "openai" {
		return false
	}
	if request.ReasoningEffort != "" || len(request.Reasoning) > 0 || len(request.Tools) == 0 {
		return false
	}
	if choice, ok := request.ToolChoice.(string); ok && choice == "none" {
		return false
	}
	return common.ChatToolsNeedReasoningOff(info.UpstreamModelName)
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return request, nil
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return request, nil
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	a.ResponseFormat = request.ResponseFormat
	if info.RelayMode == relayconstant.RelayModeAudioSpeech {
		jsonData, err := json.Marshal(request)
		if err != nil {
			return nil, fmt.Errorf("error marshalling object: %w", err)
		}
		return bytes.NewReader(jsonData), nil
	} else {
		var requestBody bytes.Buffer
		writer := multipart.NewWriter(&requestBody)

		writer.WriteField("model", request.Model)

		// 获取所有表单字段
		formData := c.Request.PostForm

		// 遍历表单字段并打印输出
		for key, values := range formData {
			if key == "model" {
				continue
			}
			for _, value := range values {
				writer.WriteField(key, value)
			}
		}

		// 添加文件字段
		file, header, err := c.Request.FormFile("file")
		if err != nil {
			return nil, errors.New("file is required")
		}
		defer file.Close()

		part, err := writer.CreateFormFile("file", header.Filename)
		if err != nil {
			return nil, errors.New("create form file failed")
		}
		if _, err := io.Copy(part, file); err != nil {
			return nil, errors.New("copy file failed")
		}

		// 关闭 multipart 编写器以设置分界线
		writer.Close()
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())
		return &requestBody, nil
	}
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	if dto.IsSeedream(request.Model) {
		if info.RelayMode == relayconstant.RelayModeImagesEdits {
			return nil, errors.New("Seedream editing uses JSON image on /v1/images/generations")
		}
		request.N = 0
		return request, nil
	}
	switch info.RelayMode {
	case relayconstant.RelayModeImagesEdits:

		var requestBody bytes.Buffer
		writer := multipart.NewWriter(&requestBody)

		writer.WriteField("model", request.Model)
		// 使用已解析的 multipart 表单，避免重复解析
		mf := c.Request.MultipartForm
		if mf == nil {
			if _, err := c.MultipartForm(); err != nil {
				return nil, errors.New("failed to parse multipart form")
			}
			mf = c.Request.MultipartForm
		}

		// 写入所有非文件字段
		if mf != nil {
			for key, values := range mf.Value {
				if key == "model" {
					continue
				}
				for _, value := range values {
					writer.WriteField(key, value)
				}
			}
		}

		if mf != nil && mf.File != nil {
			// Check if "image" field exists in any form, including array notation
			var imageFiles []*multipart.FileHeader
			var exists bool

			// First check for standard "image" field
			if imageFiles, exists = mf.File["image"]; !exists || len(imageFiles) == 0 {
				// If not found, check for "image[]" field
				if imageFiles, exists = mf.File["image[]"]; !exists || len(imageFiles) == 0 {
					// If still not found, iterate through all fields to find any that start with "image["
					foundArrayImages := false
					for fieldName, files := range mf.File {
						if strings.HasPrefix(fieldName, "image[") && len(files) > 0 {
							foundArrayImages = true
							imageFiles = append(imageFiles, files...)
						}
					}

					// If no image fields found at all
					if !foundArrayImages && (len(imageFiles) == 0) {
						return nil, errors.New("image is required")
					}
				}
			}

			// Process all image files
			for i, fileHeader := range imageFiles {
				file, err := fileHeader.Open()
				if err != nil {
					return nil, fmt.Errorf("failed to open image file %d: %w", i, err)
				}

				// If multiple images, use image[] as the field name
				fieldName := "image"
				if len(imageFiles) > 1 {
					fieldName = "image[]"
				}

				// Determine MIME type based on file extension
				mimeType := detectImageMimeType(fileHeader.Filename)

				// Create a form file with the appropriate content type
				h := make(textproto.MIMEHeader)
				h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldName, fileHeader.Filename))
				h.Set("Content-Type", mimeType)

				part, err := writer.CreatePart(h)
				if err != nil {
					return nil, fmt.Errorf("create form part failed for image %d: %w", i, err)
				}

				if _, err := io.Copy(part, file); err != nil {
					return nil, fmt.Errorf("copy file failed for image %d: %w", i, err)
				}

				// 复制完立即关闭，避免在循环内使用 defer 占用资源
				_ = file.Close()
			}

			// Handle mask file if present
			if maskFiles, exists := mf.File["mask"]; exists && len(maskFiles) > 0 {
				maskFile, err := maskFiles[0].Open()
				if err != nil {
					return nil, errors.New("failed to open mask file")
				}
				// 复制完立即关闭，避免在循环内使用 defer 占用资源

				// Determine MIME type for mask file
				mimeType := detectImageMimeType(maskFiles[0].Filename)

				// Create a form file with the appropriate content type
				h := make(textproto.MIMEHeader)
				h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="mask"; filename="%s"`, maskFiles[0].Filename))
				h.Set("Content-Type", mimeType)

				maskPart, err := writer.CreatePart(h)
				if err != nil {
					return nil, errors.New("create form file failed for mask")
				}

				if _, err := io.Copy(maskPart, maskFile); err != nil {
					return nil, errors.New("copy mask file failed")
				}
				_ = maskFile.Close()
			}
		} else {
			return nil, errors.New("no multipart form data found")
		}

		// 关闭 multipart 编写器以设置分界线
		writer.Close()
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())
		return &requestBody, nil

	default:
		return request, nil
	}
}

// detectImageMimeType determines the MIME type based on the file extension
func detectImageMimeType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		// Try to detect from extension if possible
		if strings.HasPrefix(ext, ".jp") {
			return "image/jpeg"
		}
		// Default to png as a fallback
		return "image/png"
	}
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	if request.Reasoning != nil {
		info.ReasoningEffort = request.Reasoning.Effort
	}
	if request.Temperature != nil || request.TopP != nil {
		effort := ""
		if request.Reasoning != nil {
			effort = request.Reasoning.Effort
		}
		relaycommon.WarnSamplingRefused(c, info, dropSampling(samplingRefusedWhileReasoning(info, effort), &request.Temperature, &request.TopP))
	}
	return request, nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	if info.RelayMode == relayconstant.RelayModeAudioTranscription ||
		info.RelayMode == relayconstant.RelayModeAudioTranslation ||
		info.RelayMode == relayconstant.RelayModeImagesEdits {
		return channel.DoFormRequest(a, c, info, requestBody)
	} else if info.RelayMode == relayconstant.RelayModeRealtime {
		return channel.DoWssRequest(a, c, info, requestBody)
	} else {
		return channel.DoApiRequest(a, c, info, requestBody)
	}
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.LazuError) {
	if ServesClaudeClient(info) {
		if info.IsStream {
			return ClaudeClientStreamHandler(c, resp, info)
		}
		return ClaudeClientHandler(c, resp, info)
	}
	if info.ProtocolPlan != nil {
		switch info.ProtocolPlan.UpstreamProtocol.Family {
		case textgen.ProtocolOpenAIResponses:
			if info.IsStream {
				return OaiResponsesStreamHandler(c, info, resp)
			}
			return OaiResponsesHandler(c, info, resp)
		case textgen.ProtocolOpenAIChat:
			if info.IsStream {
				return OaiStreamHandler(c, info, resp)
			}
			return OpenaiHandler(c, info, resp)
		}
	}
	switch info.RelayMode {
	case relayconstant.RelayModeRealtime:
		err, usage = OpenaiRealtimeHandler(c, info)
	case relayconstant.RelayModeAudioSpeech:
		usage = OpenaiTTSHandler(c, resp, info)
	case relayconstant.RelayModeAudioTranslation:
		fallthrough
	case relayconstant.RelayModeAudioTranscription:
		err, usage = OpenaiSTTHandler(c, resp, info, a.ResponseFormat)
	case relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeImagesEdits:
		usage, err = OpenaiHandlerWithUsage(c, info, resp)
	case relayconstant.RelayModeRerank:
		usage, err = common_handler.RerankHandler(c, info, resp)
	case relayconstant.RelayModeResponses:
		if info.IsStream {
			usage, err = OaiResponsesStreamHandler(c, info, resp)
		} else {
			usage, err = OaiResponsesHandler(c, info, resp)
		}
	default:
		if info.IsStream {
			usage, err = OaiStreamHandler(c, info, resp)
		} else {
			usage, err = OpenaiHandler(c, info, resp)
		}
	}
	return
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
