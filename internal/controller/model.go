package controller

import (
	"slices"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

// https://platform.openai.com/docs/api-reference/models/list

// Configured catalog ownership takes precedence. Name inference is only a
// fallback for models that have no assigned vendor.
func modelOwnedBy(modelName string) string {
	return modelOwnerWithCatalog(modelName, model.CatalogModelOwners([]string{modelName}))
}

func modelOwnerWithCatalog(modelName string, owners map[string]string) string {
	if owner := owners[modelName]; owner != "" {
		return owner
	}
	if vendor, _, ok := model.InferCanonicalModelBrand(modelName); ok {
		return vendor
	}
	return "custom"
}

func openAIModelForName(modelName string, catalogOwners ...map[string]string) dto.OpenAIModels {
	var owner string
	if len(catalogOwners) > 0 {
		owner = modelOwnerWithCatalog(modelName, catalogOwners[0])
	} else {
		owner = modelOwnedBy(modelName)
	}
	return dto.OpenAIModels{
		Id:                     modelName,
		Object:                 "model",
		Created:                1626777600,
		OwnedBy:                owner,
		SupportedEndpointTypes: model.GetModelSupportEndpointTypes(modelName),
	}
}

func ListModels(c *gin.Context, modelType int) {
	if modelType == constant.ChannelTypeOpenAI {
		ListModelDiscovery(c)
		return
	}

	modelNames, err := getAccessibleModelNames(c)
	if err != nil {
		writeInternalControlPlaneError(c, err)
		return
	}

	userOpenAiModels := buildOpenAIModels(modelNames)
	switch modelType {
	case constant.ChannelTypeAnthropic:
		useranthropicModels := make([]dto.AnthropicModel, len(userOpenAiModels))
		for i, model := range userOpenAiModels {
			useranthropicModels[i] = dto.AnthropicModel{
				ID:          model.Id,
				CreatedAt:   time.Unix(int64(model.Created), 0).UTC().Format(time.RFC3339),
				DisplayName: model.Id,
				Type:        "model",
			}
		}
		firstID := ""
		lastID := ""
		if len(useranthropicModels) > 0 {
			firstID = useranthropicModels[0].ID
			lastID = useranthropicModels[len(useranthropicModels)-1].ID
		}
		c.JSON(200, gin.H{
			"data":     useranthropicModels,
			"first_id": firstID,
			"has_more": false,
			"last_id":  lastID,
		})
	case constant.ChannelTypeGemini:
		userGeminiModels := make([]dto.GeminiModel, len(userOpenAiModels))
		for i, model := range userOpenAiModels {
			userGeminiModels[i] = dto.GeminiModel{
				Name:        model.Id,
				DisplayName: model.Id,
			}
		}
		c.JSON(200, gin.H{
			"models":        userGeminiModels,
			"nextPageToken": nil,
		})
	default:
		c.JSON(200, gin.H{
			"success": true,
			"data":    userOpenAiModels,
			"object":  "list",
		})
	}
}

func ListOpenAICompatibleModels(c *gin.Context) {
	modelNames, err := getAccessibleModelNames(c)
	if err != nil {
		writeInternalControlPlaneError(c, err)
		return
	}

	c.JSON(200, gin.H{
		"success": true,
		"data":    buildOpenAIModels(modelNames),
		"object":  "list",
	})
}

func ChannelListModels(c *gin.Context) {
	c.JSON(200, gin.H{
		"success": true,
		"data":    buildOpenAIModels(model.ListCatalogModelNames()),
	})
}

func EnabledListModels(c *gin.Context) {
	c.JSON(200, gin.H{
		"success": true,
		"data":    model.GetEnabledModels(),
	})
}

func RetrieveModel(c *gin.Context, modelType int) {
	if modelType == constant.ChannelTypeOpenAI {
		RetrieveModelDiscovery(c)
		return
	}

	modelId := c.Param("model")
	// Resolve against exactly what ListModels would have returned to this
	// caller. Catalog membership alone is not enough: it would answer for a
	// candidate model, or one this token's scope blocks, that the same caller
	// cannot find in the list response and cannot relay to.
	accessible, err := getAccessibleModelNames(c)
	if err != nil {
		writeInternalControlPlaneError(c, err)
		return
	}
	if slices.Contains(accessible, modelId) {
		aiModel := openAIModelForName(modelId)
		switch modelType {
		case constant.ChannelTypeAnthropic:
			c.JSON(200, dto.AnthropicModel{
				ID:          aiModel.Id,
				CreatedAt:   time.Unix(int64(aiModel.Created), 0).UTC().Format(time.RFC3339),
				DisplayName: aiModel.Id,
				Type:        "model",
			})
		default:
			c.JSON(200, aiModel)
		}
	} else {
		writeControlPlaneCatalogError(c, types.ErrorCodeModelNotFound, types.D{"model": modelId, "field": "model"})
	}
}
