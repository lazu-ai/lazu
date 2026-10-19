package router

import (
	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/model"
)

type Decision struct {
	ChannelID  int
	Model      string
	Provider   string
	Group      string
	Reason     string
	Score      float64
	Candidates []Candidate
	Meta       map[string]any
	Channel    *model.Channel
}

type Candidate struct {
	ChannelID    int
	Score        float64
	WhyNotChosen string
}

type RequestContext struct {
	RequestID       string
	UserID          int
	TokenID         int
	TokenGroup      string
	TokenMetadata   string
	SessionID       string
	AgentID         string
	GinContext      *gin.Context
	Features        *Features
	RequestedModel  string
	ResolvedModel   string
	CandidateGroups []string
	DefaultGroup    string
}
