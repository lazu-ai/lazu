package gemini

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/dto"
)

// GeminiImageReferenceMax is how many reference images a Gemini image model
// takes beside the prompt. Google documents up to 14 reference images for
// the Gemini image models (Nano Banana family); the repo has no tighter
// limit, so /v1/images/edits refuses more rather than letting the provider
// drop them silently.
const GeminiImageReferenceMax = 14

// geminiImageReferenceMaxBytes bounds one reference before base64 encoding
// (the inline_data request limit is 20 MB in total).
const geminiImageReferenceMaxBytes = 20 << 20

// geminiImageEditParts reads the reference images of an OpenAI-style
// /v1/images/edits multipart request (image, image[] or image[N] fields, in
// that order) as Gemini inline_data parts.
func geminiImageEditParts(c *gin.Context) ([]dto.GeminiPart, error) {
	if c == nil || c.Request == nil {
		return nil, errors.New("image edits need a multipart request")
	}
	form := c.Request.MultipartForm
	if form == nil {
		if _, err := c.MultipartForm(); err != nil {
			return nil, fmt.Errorf("failed to parse image edit form request: %w", err)
		}
		form = c.Request.MultipartForm
	}
	if form == nil || len(form.File) == 0 {
		return nil, errors.New("image is required")
	}
	files := geminiEditImageFiles(form.File)
	if len(files) == 0 {
		return nil, errors.New("image is required")
	}
	if len(files) > GeminiImageReferenceMax {
		return nil, fmt.Errorf("at most %d reference images are supported, got %d", GeminiImageReferenceMax, len(files))
	}
	parts := make([]dto.GeminiPart, 0, len(files))
	for index, header := range files {
		part, err := geminiInlineImagePart(header)
		if err != nil {
			return nil, fmt.Errorf("reference image %d: %w", index+1, err)
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func geminiEditImageFiles(fields map[string][]*multipart.FileHeader) []*multipart.FileHeader {
	out := make([]*multipart.FileHeader, 0)
	out = append(out, fields["image"]...)
	out = append(out, fields["image[]"]...)
	indexed := make([]string, 0)
	for name := range fields {
		if strings.HasPrefix(name, "image[") && name != "image[]" {
			indexed = append(indexed, name)
		}
	}
	sort.Slice(indexed, func(i, j int) bool {
		if len(indexed[i]) != len(indexed[j]) {
			return len(indexed[i]) < len(indexed[j])
		}
		return indexed[i] < indexed[j]
	})
	for _, name := range indexed {
		out = append(out, fields[name]...)
	}
	return out
}

func geminiInlineImagePart(header *multipart.FileHeader) (dto.GeminiPart, error) {
	if header == nil {
		return dto.GeminiPart{}, errors.New("missing file")
	}
	if header.Size > geminiImageReferenceMaxBytes {
		return dto.GeminiPart{}, errors.New("image is larger than 20 MB")
	}
	file, err := header.Open()
	if err != nil {
		return dto.GeminiPart{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, geminiImageReferenceMaxBytes+1))
	if err != nil {
		return dto.GeminiPart{}, err
	}
	if len(data) == 0 {
		return dto.GeminiPart{}, errors.New("image is empty")
	}
	if len(data) > geminiImageReferenceMaxBytes {
		return dto.GeminiPart{}, errors.New("image is larger than 20 MB")
	}
	mimeType := strings.TrimSpace(strings.Split(header.Header.Get("Content-Type"), ";")[0])
	if !strings.HasPrefix(mimeType, "image/") {
		mimeType = http.DetectContentType(data)
	}
	if !strings.HasPrefix(mimeType, "image/") {
		return dto.GeminiPart{}, fmt.Errorf("unsupported reference type %q", mimeType)
	}
	return dto.GeminiPart{InlineData: &dto.GeminiInlineData{
		MimeType: mimeType,
		Data:     base64.StdEncoding.EncodeToString(data),
	}}, nil
}
