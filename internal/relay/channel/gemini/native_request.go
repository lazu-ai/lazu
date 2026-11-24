package gemini

import (
	"bytes"
	"strconv"
	"strings"
	"unsafe"

	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/relay/wirepatch"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ConvertNativeGeminiRequest applies the same channel policy as DTO conversion
// using surgical wire edits. Untouched requests retain unknown nested fields
// (such as part-level videoMetadata and mediaResolution), explicit zero values,
// whitespace, escaping, and numeric precision exactly as supplied by the client.
func (a *Adaptor) ConvertNativeGeminiRequest(body []byte, info *relaycommon.RelayInfo) ([]byte, error) {
	var err error
	index := newWireIndex(body)
	get := func(path string) gjson.Result { return index.get(path) }
	set := func(path string, value interface{}) {
		if err != nil {
			return
		}
		existing := get(path)
		if existing.Exists() && existing.Value() == value {
			return
		}
		body, err = sjson.SetBytes(body, path, value)
		index.reset(body)
	}
	del := func(path string) {
		for err == nil && get(path).Exists() {
			body, err = sjson.DeleteBytes(body, path)
			index.reset(body)
		}
	}

	// Clean up empty systemInstruction / system_instruction.
	for _, key := range []string{"systemInstruction", "system_instruction"} {
		if sys := get(key); sys.Exists() && !hasSystemInstructionContent(sys) {
			del(key)
		}
	}

	// Strip gateway-only metadata while preserving customer values.
	if metadata := get("metadata"); metadata.Exists() && metadata.Get("lazu").Exists() {
		del("metadata.lazu")
		if remaining := get("metadata"); remaining.IsObject() && len(remaining.Map()) == 0 {
			del("metadata")
		}
	}

	// Default first turn role to "user" when omitted or empty.
	if contents := get("contents"); contents.IsArray() {
		if first := contents.Get("0"); first.IsObject() {
			if role := first.Get("role"); !role.Exists() || role.Type == gjson.Null || role.Str == "" {
				set("contents.0.role", "user")
			}
		}

		// Fill default video/webm MIME type for YouTube fileData parts lacking mimeType.
		if bytes.Contains(body, []byte("www.youtube.com")) {
			contents = get("contents")
			for i, content := range contents.Array() {
				for j, part := range content.Get("parts").Array() {
					fileKey, fileData := pickFileData(part)
					if fileKey == "" {
						continue
					}
					uri := fileData.Get("fileUri").Str
					if uri == "" {
						uri = fileData.Get("file_uri").Str
					}
					mime := fileData.Get("mimeType").Str
					if mime == "" {
						mime = fileData.Get("mime_type").Str
					}
					if mime == "" && strings.Contains(uri, "www.youtube.com") {
						mimeKey := "mimeType"
						if fileKey == "file_data" {
							mimeKey = "mime_type"
						}
						path := "contents." + strconv.Itoa(i) + ".parts." + strconv.Itoa(j) + "." + fileKey + "." + mimeKey
						set(path, "video/webm")
					}
				}
			}
		}
	}

	if err != nil {
		return nil, err
	}

	body, err = relaycommon.RemoveDisabledFields(body, info.ChannelOtherSettings)
	if err != nil {
		return nil, err
	}
	if len(info.ParamOverride) > 0 {
		body, err = relaycommon.ApplyParamOverride(body, info.ParamOverride)
		if err != nil {
			return nil, err
		}
	}
	// wire dialect patch: vendor quirks on the compiled body, once per
	// attempt, after ParamOverride (internal/relay/wirepatch).
	return wirepatch.Apply(wirepatch.ResolveDialect(info), body), nil
}

func hasSystemInstructionContent(sys gjson.Result) bool {
	if !sys.IsObject() {
		return false
	}
	parts := sys.Get("parts")
	if !parts.IsArray() {
		return false
	}
	hasContent := false
	parts.ForEach(func(_, part gjson.Result) bool {
		if !part.IsObject() {
			return true
		}
		part.ForEach(func(k, v gjson.Result) bool {
			if k.Str == "text" {
				if v.Str != "" {
					hasContent = true
					return false
				}
				return true
			}
			if v.Type != gjson.Null {
				hasContent = true
				return false
			}
			return true
		})
		return !hasContent
	})
	return hasContent
}

func pickFileData(part gjson.Result) (string, gjson.Result) {
	if fd := part.Get("fileData"); fd.IsObject() {
		return "fileData", fd
	}
	if fd := part.Get("file_data"); fd.IsObject() {
		return "file_data", fd
	}
	return "", gjson.Result{}
}

// wireIndex answers gjson paths against a JSON object body from one scan of
// its top-level keys. A path's first segment selects the field (the first
// occurrence, as gjson.GetBytes would); the rest is looked up inside that
// field's raw value only.
type wireIndex struct {
	body   []byte
	fields map[string]gjson.Result
	object bool
}

func newWireIndex(body []byte) *wireIndex { return &wireIndex{body: body} }

func (x *wireIndex) reset(body []byte) {
	x.body, x.fields = body, nil
}

func (x *wireIndex) get(path string) gjson.Result {
	if x.fields == nil {
		x.fields = map[string]gjson.Result{}
		root := gjson.Parse(unsafe.String(unsafe.SliceData(x.body), len(x.body)))
		x.object = root.IsObject()
		if x.object {
			root.ForEach(func(key, value gjson.Result) bool {
				if _, seen := x.fields[key.Str]; !seen {
					x.fields[key.Str] = value
				}
				return true
			})
		}
	}
	first, rest, nested := strings.Cut(path, ".")
	if !x.object || strings.ContainsAny(first, `*?\|#@!`) {
		return gjson.GetBytes(x.body, path)
	}
	field, ok := x.fields[first]
	if !ok {
		return gjson.Result{}
	}
	if !nested {
		return field
	}
	return field.Get(rest)
}
