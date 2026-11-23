package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
)

// ModelInfo represents a model entry returned by the models info API.
type ModelInfo struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Vendor        string          `json:"vendor"`
	VendorIcon    string          `json:"vendor_icon,omitempty"`
	DescriptionEn string          `json:"description_en"`
	DescriptionZh string          `json:"description_zh,omitempty"`
	ReleasedAt    string          `json:"released_at,omitempty"`
	ContextLength int64           `json:"context_length"`
	Modality      ModelModality   `json:"modality"`
	Tags          []string        `json:"tags"`
	Pricing       *ModelInfoPrice `json:"pricing,omitempty"`
	OpenSource    bool            `json:"open_source"`
	Source        string          `json:"source,omitempty"`
}

type ModelModality struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

type ModelInfoPrice struct {
	InputPerMillion  float64 `json:"input_per_million"`
	OutputPerMillion float64 `json:"output_per_million"`
	Currency         string  `json:"currency"`
}

// communityModelsFile is used to read/write the supplementary JSON file.
type communityModelsFile struct {
	Version   int         `json:"version"`
	UpdatedAt string      `json:"updated_at"`
	Models    []ModelInfo `json:"models"`
}

const communityModelsCacheTTL = 30 * time.Second

type cachedCommunityModels struct {
	models         []ModelInfo
	expiresAt      time.Time
	communityMtime int64
}

var (
	communityModelsCacheMu    sync.RWMutex
	communityModelsCacheState cachedCommunityModels
)

// buildModelsFromPricing converts Pricing cache data into ModelInfo entries.
func buildModelsFromPricing() []ModelInfo {
	pricingList := model.GetPricing()
	vendorList := model.GetVendors()

	// Build vendor lookup map
	vendorMap := make(map[int]model.PricingVendor, len(vendorList))
	for _, v := range vendorList {
		vendorMap[v.InternalID] = v
	}

	result := make([]ModelInfo, 0, len(pricingList))

	for _, p := range pricingList {
		// Look up vendor info
		vendorName := ""
		vendorIcon := ""
		if v, ok := vendorMap[p.InternalVendorID]; ok {
			vendorName = v.Name
			vendorIcon = v.Icon
		}
		// If model has its own icon, prefer that
		if p.Icon != "" {
			vendorIcon = p.Icon
		}

		// Format released_at; fall back to model created_at if unavailable
		releasedAt := ""
		if p.ReleasedAt != nil && *p.ReleasedAt > 0 {
			releasedAt = time.Unix(*p.ReleasedAt, 0).Format("2006-01-02")
		} else if p.CreatedAt > 0 {
			releasedAt = time.Unix(p.CreatedAt, 0).Format("2006-01-02")
		}

		// Parse tags from comma-separated string
		var tags []string
		if p.Tags != "" {
			for _, t := range strings.Split(p.Tags, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					tags = append(tags, t)
				}
			}
		}

		// Derive modality from tags
		modality := model.DeriveModality(p.Tags)

		// Determine open source from model name or tags
		isOpenSource := model.IsOpenSourceModel(p.ModelName, p.Tags)

		// Use best available description
		descEn := p.DescriptionEn
		descZh := p.DescriptionZh
		if descEn == "" {
			descEn = p.Description
		}

		info := ModelInfo{
			ID:            p.ModelName,
			Name:          p.ModelName,
			Vendor:        vendorName,
			VendorIcon:    vendorIcon,
			DescriptionEn: descEn,
			DescriptionZh: descZh,
			ReleasedAt:    releasedAt,
			ContextLength: p.ContextLength,
			Modality: ModelModality{
				Input:  modality.Input,
				Output: modality.Output,
			},
			Tags:       tags,
			OpenSource: isOpenSource,
			Source:     "system",
		}

		result = append(result, info)
	}

	// Also load supplementary community-contributed models from JSON file
	supplementary := loadCommunityJSONFile()
	existingIDs := make(map[string]bool, len(result))
	for _, m := range result {
		existingIDs[strings.ToLower(m.ID)] = true
	}
	for _, m := range supplementary {
		if !existingIDs[strings.ToLower(m.ID)] {
			m.Source = "community"
			result = append(result, m)
		}
	}

	return result
}

func getCommunityModelsFilePath() string {
	return filepath.Join(common.GetDataDir(), "community-models.json")
}

func getCommunityModelsFileMtime(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.ModTime().Unix()
}

func cloneModelInfoSlice(models []ModelInfo) []ModelInfo {
	return append([]ModelInfo(nil), models...)
}

func getCommunityModelsSnapshot() []ModelInfo {
	path := getCommunityModelsFilePath()
	mtime := getCommunityModelsFileMtime(path)
	now := time.Now()

	communityModelsCacheMu.RLock()
	if !communityModelsCacheState.expiresAt.IsZero() &&
		now.Before(communityModelsCacheState.expiresAt) &&
		communityModelsCacheState.communityMtime == mtime {
		cached := cloneModelInfoSlice(communityModelsCacheState.models)
		communityModelsCacheMu.RUnlock()
		return cached
	}
	communityModelsCacheMu.RUnlock()

	fresh := buildModelsFromPricing()
	communityModelsCacheMu.Lock()
	communityModelsCacheState.models = cloneModelInfoSlice(fresh)
	communityModelsCacheState.expiresAt = now.Add(communityModelsCacheTTL)
	communityModelsCacheState.communityMtime = mtime
	communityModelsCacheMu.Unlock()
	return fresh
}

func invalidateCommunityModelsCache() {
	communityModelsCacheMu.Lock()
	communityModelsCacheState = cachedCommunityModels{}
	communityModelsCacheMu.Unlock()
}

// loadCommunityJSONFile reads the supplementary community models JSON file.
func loadCommunityJSONFile() []ModelInfo {
	path := getCommunityModelsFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			common.SysLog(fmt.Sprintf("[community-models] failed to read %s: %v", path, err))
		}
		return nil
	}
	var file communityModelsFile
	if err := json.Unmarshal(data, &file); err != nil {
		common.SysLog(fmt.Sprintf("[community-models] failed to parse %s: %v", path, err))
		return nil
	}
	return file.Models
}

// GetPublicModelList returns all model info in an OpenRouter-compatible format for API consumers.
func GetPublicModelList(c *gin.Context) {
	allModels := getCommunityModelsSnapshot()

	// Convert to a developer-friendly format
	type publicModel struct {
		ID            string        `json:"id"`
		Name          string        `json:"name"`
		Vendor        string        `json:"vendor,omitempty"`
		Description   string        `json:"description,omitempty"`
		ContextLength int64         `json:"context_length,omitempty"`
		Modality      ModelModality `json:"modality"`
		Tags          []string      `json:"tags,omitempty"`
		ReleasedAt    string        `json:"released_at,omitempty"`
		OpenSource    bool          `json:"open_source"`
	}

	data := make([]publicModel, 0, len(allModels))
	for _, m := range allModels {
		desc := m.DescriptionEn
		if desc == "" {
			desc = m.DescriptionZh
		}
		data = append(data, publicModel{
			ID:            m.ID,
			Name:          m.Name,
			Vendor:        m.Vendor,
			Description:   desc,
			ContextLength: m.ContextLength,
			Modality:      m.Modality,
			Tags:          m.Tags,
			ReleasedAt:    m.ReleasedAt,
			OpenSource:    m.OpenSource,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    data,
	})
}
