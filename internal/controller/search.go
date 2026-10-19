package controller

import (
	"net/http"

	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

func Search(c *gin.Context) {
	var req dto.SearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeRelayError(c, types.RelayFormatOpenAI, nil, types.NewErrorWithStatusCode(err, types.ErrorCodeBadRequestBody, http.StatusBadRequest, types.ErrOptionWithSkipRetry()))
		return
	}
	resp, lazuErr := service.ExecuteSearch(c, req)
	if lazuErr != nil {
		writeRelayError(c, types.RelayFormatOpenAI, nil, lazuErr)
		return
	}
	c.JSON(http.StatusOK, resp)
}
