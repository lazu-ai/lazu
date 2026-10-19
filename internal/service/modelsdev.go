package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

// models.dev (github.com/anomalyco/models.dev, MIT) is a community catalog of
// AI models maintained by the team behind SST and opencode. Its api.json is
// keyed by provider, then by model id; a lab's own provider entry (anthropic,
// openai, zai…) carries the model's first-party facts.
const modelsDevAPIURL = "https://models.dev/api.json"

const modelsDevMaxBodyBytes = 32 << 20

type ModelsDevCatalog map[string]ModelsDevProvider

type ModelsDevProvider struct {
	ID     string                    `json:"id"`
	Name   string                    `json:"name"`
	Doc    string                    `json:"doc"`
	Models map[string]ModelsDevModel `json:"models"`
}

type ModelsDevReasoningOption struct {
	Type   string   `json:"type"`
	Values []string `json:"values,omitempty"`
}

type ModelsDevCostTier struct {
	Input      *float64 `json:"input,omitempty"`
	Output     *float64 `json:"output,omitempty"`
	CacheRead  *float64 `json:"cache_read,omitempty"`
	CacheWrite *float64 `json:"cache_write,omitempty"`
	Tier       struct {
		Type string `json:"type"`
		Size int64  `json:"size"`
	} `json:"tier"`
}

type ModelsDevCost struct {
	Input      *float64            `json:"input,omitempty"`
	Output     *float64            `json:"output,omitempty"`
	CacheRead  *float64            `json:"cache_read,omitempty"`
	CacheWrite *float64            `json:"cache_write,omitempty"`
	Tiers      []ModelsDevCostTier `json:"tiers,omitempty"`
}

type ModelsDevModel struct {
	ID               string                     `json:"id"`
	Name             string                     `json:"name"`
	Family           string                     `json:"family"`
	Description      string                     `json:"description"`
	Attachment       bool                       `json:"attachment"`
	Reasoning        bool                       `json:"reasoning"`
	ReasoningOptions []ModelsDevReasoningOption `json:"reasoning_options,omitempty"`
	ToolCall         bool                       `json:"tool_call"`
	StructuredOutput *bool                      `json:"structured_output,omitempty"`
	Temperature      *bool                      `json:"temperature,omitempty"`
	Knowledge        string                     `json:"knowledge"`
	ReleaseDate      string                     `json:"release_date"`
	LastUpdated      string                     `json:"last_updated"`
	OpenWeights      bool                       `json:"open_weights"`
	Status           string                     `json:"status"`
	Modalities       struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modalities"`
	Limit struct {
		Context int64 `json:"context"`
		Input   int64 `json:"input"`
		Output  int64 `json:"output"`
	} `json:"limit"`
	Cost *ModelsDevCost `json:"cost,omitempty"`
}

// ModelsDevEntry is one matched upstream model and the provider it came from.
type ModelsDevEntry struct {
	ProviderID string
	Model      ModelsDevModel
}

// ID is the provider-qualified id, e.g. "openai/gpt-6-sol".
func (e ModelsDevEntry) ID() string { return e.ProviderID + "/" + e.Model.ID }

// FetchModelsDevCatalog downloads the whole catalog (~5MB).
func FetchModelsDevCatalog(ctx context.Context) (ModelsDevCatalog, error) {
	client := GetHttpClient()
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(1<<attempt) * time.Second):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsDevAPIURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, modelsDevMaxBodyBytes))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("models.dev returned %d", resp.StatusCode)
			continue
		}
		var catalog ModelsDevCatalog
		if err := json.Unmarshal(body, &catalog); err != nil {
			return nil, fmt.Errorf("parse models.dev catalog: %w", err)
		}
		return catalog, nil
	}
	return nil, fmt.Errorf("fetch models.dev catalog: %w", lastErr)
}

var modelsDevDateSuffix = regexp.MustCompile(`-(\d{4}-\d{2}-\d{2}|\d{8})$`)

// modelsDevCandidates are the ids a local model name may be published under:
// itself, then without a dated snapshot suffix, then without "-latest".
func modelsDevCandidates(name string) []string {
	name = strings.ToLower(strings.TrimSpace(name))
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	out := []string{name}
	if stripped := modelsDevDateSuffix.ReplaceAllString(name, ""); stripped != name {
		out = append(out, stripped)
	}
	if stripped := strings.TrimSuffix(name, "-latest"); stripped != name {
		out = append(out, stripped)
	}
	return out
}

// ModelsDevIndex answers "which upstream entry describes this local model",
// always preferring the model's own lab over the relays that resell it.
type ModelsDevIndex struct {
	catalog ModelsDevCatalog
	byLab   map[string]map[string]ModelsDevEntry // provider id → lower model id → entry
}

func NewModelsDevIndex(catalog ModelsDevCatalog) *ModelsDevIndex {
	index := &ModelsDevIndex{catalog: catalog, byLab: make(map[string]map[string]ModelsDevEntry)}
	for providerID, provider := range catalog {
		entries := make(map[string]ModelsDevEntry, len(provider.Models))
		for modelID, upstream := range provider.Models {
			if upstream.ID == "" {
				upstream.ID = modelID
			}
			entries[strings.ToLower(modelID)] = ModelsDevEntry{ProviderID: providerID, Model: upstream}
		}
		index.byLab[providerID] = entries
	}
	return index
}

// labProviders lists the lab providers to search, the model's own maker
// first, then every other registered lab.
func (i *ModelsDevIndex) labProviders(maker string) []string {
	out := []string{}
	seen := map[string]struct{}{}
	add := func(ids []string) {
		for _, id := range ids {
			if _, ok := seen[id]; ok {
				continue
			}
			if _, ok := i.catalog[id]; !ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	if own, ok := model.ModelMakerByKey(maker); ok {
		add(own.ModelsDevProviders)
	}
	for _, other := range model.KnownModelMakers() {
		add(other.ModelsDevProviders)
	}
	return out
}

// Lookup finds the upstream entry for a local model. An explicit
// "provider/model" id pins the match; otherwise lab providers are searched.
func (i *ModelsDevIndex) Lookup(localName, maker, pinnedID string) (ModelsDevEntry, bool) {
	if i == nil {
		return ModelsDevEntry{}, false
	}
	if pinned := strings.TrimSpace(pinnedID); pinned != "" {
		if slash := strings.Index(pinned, "/"); slash > 0 {
			if entry, ok := i.byLab[pinned[:slash]][strings.ToLower(pinned[slash+1:])]; ok {
				return entry, true
			}
		}
		return ModelsDevEntry{}, false
	}
	for _, candidate := range modelsDevCandidates(localName) {
		for _, providerID := range i.labProviders(maker) {
			if entry, ok := i.byLab[providerID][candidate]; ok {
				return entry, true
			}
		}
	}
	return ModelsDevEntry{}, false
}

// RecentLabReleases lists lab models released on or after since, for the
// given makers, skipping rolling aliases.
func (i *ModelsDevIndex) RecentLabReleases(makers []string, since string) []ModelsDevEntryWithMaker {
	out := []ModelsDevEntryWithMaker{}
	for _, makerKey := range makers {
		maker, ok := model.ModelMakerByKey(makerKey)
		if !ok {
			continue
		}
		seen := map[string]struct{}{}
		for _, providerID := range maker.ModelsDevProviders {
			for modelID, entry := range i.byLab[providerID] {
				if entry.Model.ReleaseDate < since || entry.Model.Status == "deprecated" {
					continue
				}
				// Rolling aliases, dated snapshots and "@variant" rows describe a
				// model already counted under its plain id.
				if strings.HasSuffix(modelID, "-latest") || modelsDevDateSuffix.MatchString(modelID) || strings.Contains(modelID, "@") {
					continue
				}
				if _, dup := seen[modelID]; dup {
					continue
				}
				// A lab's provider entry can also resell other labs' models
				// (Alibaba serves Kimi and DeepSeek); those are not its releases.
				if named := model.ResolveModelMaker("", modelID, ""); named != "" && named != maker.Key {
					continue
				}
				seen[modelID] = struct{}{}
				out = append(out, ModelsDevEntryWithMaker{Maker: maker.Key, Entry: entry})
			}
		}
	}
	return out
}

type ModelsDevEntryWithMaker struct {
	Maker string
	Entry ModelsDevEntry
}

func logModelsDev(message string) {
	common.SysLog("[models.dev] " + message)
}
