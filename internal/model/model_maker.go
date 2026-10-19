package model

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"

	"github.com/lazu-ai/lazu/internal/common"
)

// A model maker is the lab that trained a model — Anthropic, OpenAI, DeepSeek.
// It is deliberately not the vendors table: a vendor row is whoever a model
// was imported from (Vivgrid, aliyun-bailian, a Cloudflare account) and Keys
// pin lanes by vendor id, so it can neither be trusted for grouping nor be
// rewritten to fix grouping.
type ModelMaker struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Upstream provider ids in the models.dev catalog that publish this lab's
	// own models, most authoritative first.
	ModelsDevProviders []string `json:"-"`
	namePattern        *regexp.Regexp
	vendorPattern      *regexp.Regexp
}

// knownModelMakers lists every lab the console can name, in the default
// reading order. Operators reorder them with the ModelMakerOrder option.
var knownModelMakers = []ModelMaker{
	{Key: "anthropic", Label: "Anthropic", ModelsDevProviders: []string{"anthropic"},
		namePattern: regexp.MustCompile(`(?i)^claude`), vendorPattern: regexp.MustCompile(`(?i)anthropic|claude`)},
	{Key: "openai", Label: "OpenAI", ModelsDevProviders: []string{"openai"},
		namePattern:   regexp.MustCompile(`(?i)^(gpt|o[1345](-|$)|chatgpt|codex|dall-e|whisper|tts-|text-embedding|sora|omni-moderation)`),
		vendorPattern: regexp.MustCompile(`(?i)^openai$`)},
	{Key: "google", Label: "Google", ModelsDevProviders: []string{"google", "google-vertex"},
		namePattern:   regexp.MustCompile(`(?i)^(gemini|gemma|imagen|veo|learnlm)`),
		vendorPattern: regexp.MustCompile(`(?i)^(google|gemini|vertex)`)},
	{Key: "deepseek", Label: "DeepSeek", ModelsDevProviders: []string{"deepseek"},
		namePattern: regexp.MustCompile(`(?i)^deepseek`), vendorPattern: regexp.MustCompile(`(?i)deepseek`)},
	{Key: "moonshot", Label: "Moonshot AI", ModelsDevProviders: []string{"moonshotai", "moonshotai-cn"},
		namePattern: regexp.MustCompile(`(?i)^(kimi|moonshot)`), vendorPattern: regexp.MustCompile(`(?i)moonshot|kimi`)},
	{Key: "zhipu", Label: "Zhipu AI", ModelsDevProviders: []string{"zai", "zhipuai"},
		namePattern: regexp.MustCompile(`(?i)^(glm|chatglm|cogview|cogvideo)`), vendorPattern: regexp.MustCompile(`(?i)zhipu|z\.ai|^zai$`)},
	{Key: "alibaba", Label: "Alibaba", ModelsDevProviders: []string{"alibaba", "alibaba-cn"},
		namePattern: regexp.MustCompile(`(?i)^(qwen|qwq|qvq|tongyi|wan[0-9.-])`), vendorPattern: regexp.MustCompile(`(?i)alibaba|aliyun|bailian|dashscope|qwen`)},
	{Key: "minimax", Label: "MiniMax", ModelsDevProviders: []string{"minimax", "minimax-cn"},
		namePattern: regexp.MustCompile(`(?i)^(minimax|abab)`), vendorPattern: regexp.MustCompile(`(?i)minimax`)},
	{Key: "xai", Label: "xAI", ModelsDevProviders: []string{"xai"},
		namePattern: regexp.MustCompile(`(?i)^grok`), vendorPattern: regexp.MustCompile(`(?i)^xai$`)},
	{Key: "meta", Label: "Meta", ModelsDevProviders: []string{"meta", "llama"},
		namePattern: regexp.MustCompile(`(?i)^(llama|meta-llama)`), vendorPattern: regexp.MustCompile(`(?i)^meta$`)},
	{Key: "mistral", Label: "Mistral", ModelsDevProviders: []string{"mistral"},
		namePattern: regexp.MustCompile(`(?i)^(mistral|mixtral|codestral|magistral|devstral|pixtral|ministral)`), vendorPattern: regexp.MustCompile(`(?i)mistral`)},
	{Key: "bytedance", Label: "ByteDance", ModelsDevProviders: []string{"volcengine"},
		namePattern: regexp.MustCompile(`(?i)^(doubao|seed-|seedream|seedance)`), vendorPattern: regexp.MustCompile(`(?i)bytedance|volcengine|doubao`)},
	{Key: "xiaomi", Label: "Xiaomi", ModelsDevProviders: []string{"xiaomi"},
		namePattern: regexp.MustCompile(`(?i)^mimo`), vendorPattern: regexp.MustCompile(`(?i)xiaomi`)},
	{Key: "stepfun", Label: "StepFun", ModelsDevProviders: []string{"stepfun"},
		namePattern: regexp.MustCompile(`(?i)^step-`), vendorPattern: regexp.MustCompile(`(?i)stepfun`)},
	{Key: "cohere", Label: "Cohere", ModelsDevProviders: []string{"cohere"},
		namePattern: regexp.MustCompile(`(?i)^(command|embed-|rerank-)`), vendorPattern: regexp.MustCompile(`(?i)cohere`)},
}

// defaultModelMakerOrder is the order the product ships with: the three
// frontier labs, DeepSeek, then the Chinese labs, then xAI.
var defaultModelMakerOrder = []string{"anthropic", "openai", "google", "deepseek", "moonshot", "zhipu", "alibaba", "minimax", "xai"}

// ModelMakerOrderOptionKey stores the operator's ranked maker keys as a JSON
// array. Makers it omits are "unranked" and read after it, by name.
const ModelMakerOrderOptionKey = "ModelMakerOrder"

var (
	modelMakerOrderMu sync.RWMutex
	modelMakerOrder   = append([]string(nil), defaultModelMakerOrder...)
)

// KnownModelMakers returns the registry in its declared order.
func KnownModelMakers() []ModelMaker {
	out := make([]ModelMaker, len(knownModelMakers))
	copy(out, knownModelMakers)
	return out
}

// ModelMakerByKey finds a registered maker.
func ModelMakerByKey(key string) (ModelMaker, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, maker := range knownModelMakers {
		if maker.Key == key {
			return maker, true
		}
	}
	return ModelMaker{}, false
}

// ModelMakerLabel is the maker's display name, or the key itself.
func ModelMakerLabel(key string) string {
	if maker, ok := ModelMakerByKey(key); ok {
		return maker.Label
	}
	return key
}

// ResolveModelMaker names the lab behind a model: an explicit maker wins,
// then the model name (which never lies about its lab), then the vendor name
// as a last resort. An unrecognised model has no maker.
func ResolveModelMaker(explicit, modelName, vendorName string) string {
	if key := strings.ToLower(strings.TrimSpace(explicit)); key != "" {
		return key
	}
	name := strings.TrimSpace(modelName)
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	for _, maker := range knownModelMakers {
		if maker.namePattern != nil && maker.namePattern.MatchString(name) {
			return maker.Key
		}
	}
	vendor := strings.TrimSpace(vendorName)
	if vendor == "" {
		return ""
	}
	for _, maker := range knownModelMakers {
		if maker.vendorPattern != nil && maker.vendorPattern.MatchString(vendor) {
			return maker.Key
		}
	}
	return ""
}

// ModelMakerOrder returns the operator's ranked maker keys.
func ModelMakerOrder() []string {
	modelMakerOrderMu.RLock()
	defer modelMakerOrderMu.RUnlock()
	return append([]string(nil), modelMakerOrder...)
}

// ModelMakerRank is a maker's place in the ranked order. Unranked and unknown
// makers share the rank past the end and fall back to name order.
func ModelMakerRank(key string) int {
	key = strings.ToLower(strings.TrimSpace(key))
	modelMakerOrderMu.RLock()
	defer modelMakerOrderMu.RUnlock()
	for index, ranked := range modelMakerOrder {
		if ranked == key {
			return index
		}
	}
	return len(modelMakerOrder)
}

// NormalizeModelMakerOrder keeps registered keys once each, in the given order.
func NormalizeModelMakerOrder(keys []string) []string {
	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.ToLower(strings.TrimSpace(key))
		if _, ok := ModelMakerByKey(key); !ok {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}

// applyModelMakerOrderOption loads the option value into memory. An empty or
// unreadable value restores the shipped default rather than ranking nothing.
func applyModelMakerOrderOption(raw string) {
	order := append([]string(nil), defaultModelMakerOrder...)
	if strings.TrimSpace(raw) != "" {
		var keys []string
		if err := json.Unmarshal([]byte(raw), &keys); err != nil {
			common.SysLog("invalid " + ModelMakerOrderOptionKey + " option: " + err.Error())
		} else {
			order = NormalizeModelMakerOrder(keys)
		}
	}
	modelMakerOrderMu.Lock()
	modelMakerOrder = order
	modelMakerOrderMu.Unlock()
}

// EncodeModelMakerOrder is the option value for a ranked list.
func EncodeModelMakerOrder(keys []string) string {
	raw, _ := json.Marshal(NormalizeModelMakerOrder(keys))
	return string(raw)
}
