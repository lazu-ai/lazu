package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/dto"
)

const ossURLFilesContextKey = "oss_url_files_cache"

// ApplyOSSURLToRequest enriches supported request types with OSS_URL attachments.
func ApplyOSSURLToRequest(c *gin.Context, request dto.Request, modelName string) error {
	switch req := request.(type) {
	case *dto.GeneralOpenAIRequest:
		return InjectOSSURLMedia(c, req, modelName)
	case *dto.GeminiChatRequest:
		return InjectOSSURLGemini(c, req, modelName)
	default:
		return nil
	}
}

// InjectOSSURLMedia downloads files referenced via OSS_URL and appends them to the OpenAI-compatible request messages.
func InjectOSSURLMedia(c *gin.Context, req *dto.GeneralOpenAIRequest, modelName string) error {
	files, err := FetchOSSURLFiles(c, modelName, nil)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	medias := make([]dto.MediaContent, 0, len(files))
	for _, file := range files {
		medias = append(medias, mediaContentFromFile(file))
	}
	appendMediaToLastUserMessage(req, medias)
	return nil
}

// InjectOSSURLGemini applies OSS_URL files to the Gemini chat request as inline data parts.
func InjectOSSURLGemini(c *gin.Context, req *dto.GeminiChatRequest, modelName string) error {
	files, err := FetchOSSURLFiles(c, modelName, nil)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	parts := make([]dto.GeminiPart, 0, len(files))
	for _, file := range files {
		parts = append(parts, geminiPartFromFile(file))
	}
	appendGeminiPartsToLastUser(req, parts)
	return nil
}

// FetchOSSURLFiles downloads and caches OSS_URL files for the current request.
func FetchOSSURLFiles(c *gin.Context, modelName string, explicit []string) ([]*OSSURLFile, error) {
	if cached, ok := c.Get(ossURLFilesContextKey); ok {
		if files, ok := cached.([]*OSSURLFile); ok {
			return files, nil
		}
	}
	urls := explicit
	if urls == nil {
		urls = extractOSSURLValues(c)
	}
	urls = normalizeOSSURLValues(urls)
	if len(urls) == 0 {
		return nil, nil
	}
	requestID := c.GetString(common.RequestIdKey)
	files := make([]*OSSURLFile, 0, len(urls))
	for _, raw := range urls {
		file, err := resolveOSSURLFile(c, requestID, raw, modelName)
		if err != nil {
			return nil, fmt.Errorf("oss_url '%s': %w", raw, err)
		}
		files = append(files, file)
	}
	c.Set(ossURLFilesContextKey, files)
	return files, nil
}

func resolveOSSURLFile(c *gin.Context, requestID, rawURL, modelName string) (*OSSURLFile, error) {
	cacheAny, _ := c.Get(ossDownloadCacheKey)
	cache, _ := cacheAny.(map[string]*OSSURLFile)
	if cache != nil {
		if file, ok := cache[rawURL]; ok {
			return file, nil
		}
	} else {
		cache = make(map[string]*OSSURLFile)
		c.Set(ossDownloadCacheKey, cache)
	}
	file, err := downloadAndStageOSSFile(requestID, rawURL, modelName)
	if err != nil {
		return nil, err
	}
	cache[rawURL] = file
	return file, nil
}

func extractOSSURLValues(c *gin.Context) []string {
	if cached, ok := c.Get(ossURLContextKey); ok {
		if values, ok := cached.([]string); ok {
			return values
		}
	}
	body, err := common.GetRequestBody(c)
	if err != nil || len(body) == 0 {
		return nil
	}
	values := parseOSSURLFromBody(body)
	c.Set(ossURLContextKey, values)
	return values
}

func parseOSSURLFromBody(body []byte) []string {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	return normalizeOSSURLValues(extractURLsFromJSON(raw))
}

func extractURLsFromJSON(raw map[string]json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	keys := []string{"OSS_URL", "oss_url", "OSS_URLS", "oss_urls", "ossUrl", "ossUrls"}
	result := make([]string, 0)
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		result = append(result, decodeOSSValue(value)...)
	}
	return result
}

func decodeOSSValue(raw json.RawMessage) []string {
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}
	}
	return nil
}

func normalizeOSSURLValues(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	dedup := make(map[string]struct{})
	normalized := make([]string, 0, len(values))
	for _, item := range values {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if _, ok := dedup[trimmed]; ok {
			continue
		}
		dedup[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

func mediaContentFromFile(file *OSSURLFile) dto.MediaContent {
	if file == nil {
		return dto.MediaContent{Type: dto.ContentTypeText, Text: ""}
	}
	if strings.HasPrefix(file.MimeType, "image/") {
		return dto.MediaContent{
			Type: dto.ContentTypeImageURL,
			ImageUrl: &dto.MessageImageUrl{
				Url:      fmt.Sprintf("data:%s;base64,%s", file.MimeType, file.Base64Data),
				Detail:   "high",
				MimeType: file.MimeType,
			},
		}
	}
	if strings.HasPrefix(file.MimeType, "audio/") {
		format := strings.TrimPrefix(file.MimeType, "audio/")
		if format == "" {
			format = "wav"
		}
		return dto.MediaContent{
			Type: dto.ContentTypeInputAudio,
			InputAudio: &dto.MessageInputAudio{
				Data:   file.Base64Data,
				Format: format,
			},
		}
	}
	return dto.MediaContent{
		Type: dto.ContentTypeFile,
		File: &dto.MessageFile{
			FileName: file.FileName,
			FileData: fmt.Sprintf("data:%s;base64,%s", file.MimeType, file.Base64Data),
		},
	}
}

func geminiPartFromFile(file *OSSURLFile) dto.GeminiPart {
	return dto.GeminiPart{
		InlineData: &dto.GeminiInlineData{
			MimeType: file.MimeType,
			Data:     file.Base64Data,
		},
	}
}

func appendMediaToLastUserMessage(req *dto.GeneralOpenAIRequest, medias []dto.MediaContent) {
	if len(req.Messages) == 0 {
		req.Messages = append(req.Messages, dto.Message{Role: "user"})
	}
	target := len(req.Messages) - 1
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			target = i
			break
		}
	}
	message := &req.Messages[target]
	existing := message.ParseContent()
	if len(existing) == 0 && message.IsStringContent() {
		existing = []dto.MediaContent{{Type: dto.ContentTypeText, Text: message.StringContent()}}
	}
	existing = append(existing, medias...)
	message.SetMediaContent(existing)
}

func appendGeminiPartsToLastUser(req *dto.GeminiChatRequest, parts []dto.GeminiPart) {
	if len(req.Contents) == 0 {
		req.Contents = append(req.Contents, dto.GeminiChatContent{Role: "user"})
	}
	target := len(req.Contents) - 1
	for i := len(req.Contents) - 1; i >= 0; i-- {
		role := strings.ToLower(strings.TrimSpace(req.Contents[i].Role))
		if role == "user" || role == "" {
			target = i
			break
		}
	}
	content := &req.Contents[target]
	if strings.TrimSpace(content.Role) == "" {
		content.Role = "user"
	}
	content.Parts = append(content.Parts, parts...)
}
