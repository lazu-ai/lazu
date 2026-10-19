package controller

import (
	"net/http"

	"github.com/lazu-ai/lazu/internal/model"

	"github.com/gin-gonic/gin"
)

func GetGroups(c *gin.Context) {
	groupNames := make([]string, 0)
	groupNames = append(groupNames, model.GetPricingChannelGroups()...)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    groupNames,
	})
}
