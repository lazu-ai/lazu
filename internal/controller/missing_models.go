package controller

import (
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"

	"github.com/gin-gonic/gin"
)

// GetMissingModels returns the list of model names that are referenced by channels
// but do not have corresponding records in the models meta table.
// This helps administrators quickly discover models that need configuration.
func GetMissingModels(c *gin.Context) {
	missing, err := model.GetMissingModels()
	if err != nil {
		httperr.Write(c, err)
		return
	}
	common.ApiSuccess(c, missing)
}
