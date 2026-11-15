package dto

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/lazu-ai/lazu/internal/types"
)

// DecisionAsset is the JSON form of a TypeSafe image asset. Multipart and
// remote URLs are deliberately not part of this contract.
type DecisionAsset struct {
	Type       string `json:"type,omitempty"`
	Format     string `json:"format"`
	DataBase64 string `json:"data_base64"`
}

func (a *DecisionAsset) UnmarshalJSON(data []byte) error {
	type wire DecisionAsset
	var value wire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*a = DecisionAsset(value)
	return nil
}

func (r *DecisionRequest) validateAssets(caps *DecisionCapabilities) error {
	if len(r.Assets) == 0 {
		return nil
	}
	fail := func(message string) error { return decisionError(types.ErrorCodeUnsupportedDecisionInput, message) }
	if !caps.SupportsAssets {
		return fail("assets are supported only by Jev decisions")
	}
	if len(r.Images) > 0 {
		return fail("use either assets or images, not both")
	}
	if len(r.Assets) > caps.MaxImages {
		return fail("assets exceed the 4 image limit")
	}
	if len(r.Questions) == 0 {
		return fail("assets require explicit questions; question generation is unsupported")
	}
	// Validate only business string values, never keys or the assets map itself.
	refs := make(map[string]int, len(r.Assets))
	for reference := range r.Assets {
		if strings.TrimSpace(reference) == "" || !utf8.ValidString(reference) || len(reference) > 1024 {
			return fail("asset references must be nonempty UTF-8 strings of at most 1024 bytes")
		}
		refs[reference] = 0
	}
	countAssetReferences(r.State, refs)
	for _, raw := range r.Questions {
		var question DecisionQuestion
		if err := json.Unmarshal(raw, &question); err != nil {
			return err
		}
		countAssetReferences(question.Instructions, refs)
		if len(question.Criteria) > 0 {
			countAssetReferences(question.Criteria, refs)
		}
	}
	// Stable order keeps logs and metadata deterministic without changing the
	// original JSON or its reference positions.
	names := make([]string, 0, len(r.Assets))
	for name := range r.Assets {
		names = append(names, name)
	}
	sort.Strings(names)
	infos := make([]DecisionImage, 0, len(names))
	if r.assetImages == nil {
		r.assetImages = make(map[string]DecisionImage, len(names))
	}
	total, expanded := 0, 0
	for _, name := range names {
		asset := r.Assets[name]
		if refs[name] == 0 {
			return fail("every asset must be referenced in state, instructions or criteria")
		}
		if asset.Type != "" && asset.Type != "image" {
			return fail("asset type must be image")
		}
		format := asset.Format
		if format == "jpg" {
			format = "jpeg"
		}
		if format != "png" && format != "jpeg" {
			return fail("Jev assets support only PNG and JPEG")
		}
		if strings.IndexAny(asset.DataBase64, " \t\r\n") >= 0 {
			return fail("asset base64 must not contain whitespace")
		}
		img, cached := r.assetImages[name]
		if !cached {
			raw, _ := json.Marshal("data:image/" + format + ";base64," + asset.DataBase64)
			var err error
			img, err = ParseDecisionImage(raw)
			if err != nil {
				return err
			}
			if err := validateJevImage(img); err != nil {
				return err
			}
			// The parsed input is immutable; later validation checks questions again
			// without repeatedly decoding the same pixels.
			r.assetImages[name] = img
		}
		total += img.Bytes
		// Count repeated references as repeated inputs. This local encoded-file
		// bound is conservative for overlaps; provider re-encoding limits still apply.
		if refs[name] > (8<<20)/img.Bytes {
			return fail("expanded image inputs exceed 8 MiB")
		}
		expanded += refs[name] * img.Bytes
		if total > 8<<20 || expanded > 8<<20 {
			return fail("decoded or expanded image inputs exceed 8 MiB")
		}
		infos = append(infos, img)
	}
	r.ImageInfo = infos
	return nil
}

func countAssetReferences(raw json.RawMessage, refs map[string]int) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return
	}
	switch raw[0] {
	case '"':
		var value string
		if json.Unmarshal(raw, &value) == nil {
			// Whole values take precedence over substring matches, as in TypeSafe.
			if _, ok := refs[value]; ok {
				refs[value]++
				return
			}
			for ref := range refs {
				refs[ref] += strings.Count(value, ref)
			}
		}
	case '{':
		var values map[string]json.RawMessage
		if json.Unmarshal(raw, &values) == nil {
			for _, value := range values {
				countAssetReferences(value, refs)
			}
		}
	case '[':
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) == nil {
			for _, value := range values {
				countAssetReferences(value, refs)
			}
		}
	}
}

// Limits checked against https://docs.typesafe.ai/images (2026-11-15).
// Lazu retains its lower 4 MiB/file, 8 MiB decoded and 13 MiB wire limits.
func validateJevImage(img DecisionImage) error {
	fail := func() error {
		return decisionError(types.ErrorCodeUnsupportedDecisionInput, "Jev requires a valid still PNG/JPEG with each side between 280 and 1568 pixels")
	}
	if (img.ContentType != "image/png" && img.ContentType != "image/jpeg") || img.Width < 280 || img.Height < 280 || img.Width > 1568 || img.Height > 1568 {
		return fail()
	}
	// Decode after bounded dimensions to reject truncated/corrupt files. Scan PNG
	// chunks rather than searching compressed pixels for the APNG acTL marker.
	data := strings.SplitN(img.DataURL, ",", 2)
	if len(data) != 2 {
		return fail()
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(data[1])
	if err != nil {
		return fail()
	}
	if img.ContentType == "image/png" {
		for offset := 8; offset+12 <= len(decoded); {
			size := int64(binary.BigEndian.Uint32(decoded[offset : offset+4]))
			if size > int64(len(decoded)-offset-12) {
				return fail()
			}
			if string(decoded[offset+4:offset+8]) == "acTL" {
				return fail()
			}
			offset += int(size) + 12
		}
	}
	if _, _, err := image.Decode(bytes.NewReader(decoded)); err != nil {
		return fail()
	}
	return nil
}

// JevImagePayload preserves explicit asset references. The legacy shared
// images convenience form is wrapped in a documented shared state envelope;
// it never mutates the parsed request, so a fallback sees the same input.
func (r *DecisionRequest) JevImagePayload() (json.RawMessage, map[string]DecisionAsset, error) {
	if len(r.Assets) > 0 || len(r.ImageInfo) == 0 {
		return r.State, r.Assets, nil
	}
	assets := make(map[string]DecisionAsset, len(r.ImageInfo))
	references := make([]string, 0, len(r.ImageInfo))
	for _, img := range r.ImageInfo {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, nil, err
		}
		ref := "<image:" + hex.EncodeToString(nonce[:]) + ">"
		parts := strings.SplitN(img.DataURL, ",", 2)
		if len(parts) != 2 {
			return nil, nil, fmt.Errorf("image data is missing")
		}
		assets[ref] = DecisionAsset{Type: "image", Format: strings.TrimPrefix(img.ContentType, "image/"), DataBase64: parts[1]}
		references = append(references, ref)
	}
	state, err := json.Marshal(struct {
		State  json.RawMessage `json:"state"`
		Images []string        `json:"images"`
	}{r.State, references})
	return state, assets, err
}
