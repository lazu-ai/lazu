package textgen

import (
	"strings"

	"github.com/lazu-ai/lazu/internal/types"
)

// TokenCountMeta projects the protocol-neutral request into the existing token
// estimator contract. Media payloads stay in Files so callers can choose a
// bounded estimate without scanning or decoding their contents.
func TokenCountMeta(request *Request) *types.TokenCountMeta {
	meta := &types.TokenCountMeta{}
	if request == nil {
		return meta
	}
	texts := make([]string, 0, len(request.Conversation)*2+len(request.Instructions)*2+len(request.Tools)*3)
	for _, instruction := range request.Instructions {
		texts = appendNonEmpty(texts, instruction.Role, instruction.Text)
	}
	for _, item := range request.Conversation {
		if item.Kind == InputMessage {
			meta.MessagesCount++
		}
		texts = appendNonEmpty(texts, item.Role, item.Name, item.FunctionName, item.Arguments, item.Output)
		if item.PlainReasoning.Set {
			texts = appendNonEmpty(texts, item.PlainReasoning.Value)
		}
		if item.Name != "" {
			meta.NameCount++
		}
		for _, part := range item.Content {
			switch part.Kind {
			case ContentText:
				texts = appendNonEmpty(texts, part.Text)
			case ContentImage:
				meta.Files = append(meta.Files, &types.FileMeta{FileType: types.FileTypeImage, OriginData: firstNonEmpty(part.URL, part.Data), Detail: part.Detail})
			case ContentAudio:
				meta.Files = append(meta.Files, &types.FileMeta{FileType: types.FileTypeAudio, OriginData: firstNonEmpty(part.URL, part.Data)})
			case ContentFile:
				meta.Files = append(meta.Files, &types.FileMeta{FileType: types.FileTypeFile, OriginData: firstNonEmpty(part.URL, part.Data, part.FileID)})
			}
		}
	}
	for _, tool := range request.Tools {
		meta.ToolsCount++
		texts = appendNonEmpty(texts, tool.Name, tool.Description, string(tool.Parameters), string(tool.Hosted))
	}
	texts = appendNonEmpty(texts, request.Output.Name, request.Output.Description, string(request.Output.Schema), string(request.Metadata.Metadata))
	meta.CombineText = strings.Join(texts, "\n")
	if request.Sampling.MaxOutput.Set {
		meta.MaxTokens = int(request.Sampling.MaxOutput.Value)
	}
	if request.Sampling.N.Set {
		meta.CompletionCount = request.Sampling.N.Value
	}
	return meta
}

func appendNonEmpty(target []string, values ...string) []string {
	for _, value := range values {
		if value != "" {
			target = append(target, value)
		}
	}
	return target
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
