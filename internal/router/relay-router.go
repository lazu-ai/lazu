package router

import (
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/controller"
	filecontroller "github.com/lazu-ai/lazu/internal/controller/files"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/protocol/textgen"
	relayfiles "github.com/lazu-ai/lazu/internal/relay/files"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

func SetRelayRouter(router *gin.Engine) {
	router.Use(common.TrackRelayTiming(service.RecordRelayTimingDiagnostics), middleware.DecompressRequestMiddleware())
	// https://platform.openai.com/docs/api-reference/introduction
	modelsRouter := router.Group("/v1/models")
	modelsRouter.Use(middleware.OptionalTokenAuth(), middleware.RejectEphemeralCredentialOutsideRelay())
	{
		modelsRouter.GET("", func(c *gin.Context) {
			switch {
			case c.GetHeader("x-api-key") != "" && c.GetHeader("anthropic-version") != "":
				controller.ListModels(c, constant.ChannelTypeAnthropic)
			default:
				controller.ListModels(c, constant.ChannelTypeOpenAI)
			}
		})

		modelsRouter.GET("/:model", func(c *gin.Context) {
			switch {
			case c.GetHeader("x-api-key") != "" && c.GetHeader("anthropic-version") != "":
				controller.RetrieveModel(c, constant.ChannelTypeAnthropic)
			default:
				controller.RetrieveModel(c, constant.ChannelTypeOpenAI)
			}
		})
	}

	geminiRouter := router.Group("/v1beta/models")
	geminiRouter.Use(middleware.TokenAuth())
	geminiRouter.Use(middleware.RejectEphemeralCredentialOutsideRelay())
	{
		geminiRouter.GET("", func(c *gin.Context) {
			controller.ListModels(c, constant.ChannelTypeGemini)
		})
	}

	geminiCompatibleRouter := router.Group("/v1beta/openai/models")
	geminiCompatibleRouter.Use(middleware.TokenAuth())
	geminiCompatibleRouter.Use(middleware.RejectEphemeralCredentialOutsideRelay())
	{
		geminiCompatibleRouter.GET("", func(c *gin.Context) {
			controller.ListOpenAICompatibleModels(c)
		})
	}

	playgroundRouter := router.Group("/pg")
	playgroundRouter.Use(middleware.SystemPerformanceCheck())
	playgroundRouter.Use(middleware.RelayRequestBodyLimit())
	playgroundRouter.Use(middleware.UserAuth(), middleware.Distribute())
	{
		playgroundRouter.POST("/chat/completions", controller.Playground)
	}
	relayV1Router := router.Group("/v1")
	relayV1Router.Use(middleware.SystemPerformanceCheck())
	relayV1Router.Use(middleware.RelayRequestBodyLimit())
	relayV1Router.Use(middleware.TokenAuth())
	service.SetDecisionGenerationEngine(router)
	{
		decisionsRouter := router.Group("/v1")
		decisionsRouter.Use(controller.DecisionsEnabled(), middleware.SystemPerformanceCheck(), middleware.RelayRequestBodyLimit(), middleware.TokenAuth(), controller.ParseDecisions(), middleware.ModelRequestRateLimit(), middleware.SuppressRelayTelemetry(), middleware.Distribute())
		decisionsRouter.POST("/decisions", controller.Decisions)
	}
	{
		searchRouter := relayV1Router.Group("")
		searchRouter.Use(middleware.RejectEphemeralCredentialOutsideRelay())
		searchRouter.Use(middleware.ModelRequestRateLimit())
		searchRouter.POST("/search", controller.Search)
	}
	{
		filesRouter := relayV1Router.Group("/files")
		filesRouter.Use(middleware.RejectEphemeralCredentialOutsideRelay())
		filesRouter.GET("", filecontroller.List)
		filesRouter.POST("", filecontroller.Upload)
		filesRouter.DELETE("/:id", filecontroller.Delete)
		filesRouter.GET("/:id", filecontroller.Retrieve)
		filesRouter.GET("/:id/content", filecontroller.Content)
	}
	{
		mediaJobsRouter := relayV1Router.Group("/media/jobs")
		mediaJobsRouter.Use(middleware.RejectEphemeralCredentialOutsideRelay())
		mediaJobsRouter.GET("", controller.ListMediaJobs)
		mediaJobsRouter.GET("/:id", controller.GetMediaJob)
	}
	{
		mediaJobCreateRouter := relayV1Router.Group("/media/jobs")
		mediaJobCreateRouter.Use(middleware.RejectEphemeralCredentialOutsideRelay())
		mediaJobCreateRouter.Use(middleware.ModelRequestRateLimit())
		mediaJobCreateRouter.POST("", controller.CreateMediaJob)
	}
	{
		videosRouter := relayV1Router.Group("/videos")
		videosRouter.GET("", controller.ListVideoJobs)
		videosRouter.GET("/:id", controller.GetVideoJob)
		videosRouter.GET("/:id/content", controller.GetVideoContent)
		videosRouter.GET("/:id/events", controller.StreamVideoJobEvents)
	}
	{
		// WebSocket 路由（统一到 Relay）
		wsRouter := relayV1Router.Group("")
		wsRouter.Use(middleware.ModelRequestRateLimit())
		wsRouter.Use(middleware.Distribute())
		wsRouter.GET("/realtime", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIRealtime)
		})
	}
	{
		videoCreateRouter := relayV1Router.Group("")
		videoCreateRouter.Use(middleware.ModelRequestRateLimit())
		videoCreateRouter.Use(middleware.SuppressRelayTelemetry())
		videoCreateRouter.Use(middleware.Distribute())
		videoCreateRouter.POST("/videos", controller.SubmitVideoJob)
	}
	{
		responsesRouter := relayV1Router.Group("")
		responsesRouter.Use(middleware.ModelRequestRateLimit())
		responsesRouter.Use(relayfiles.DereferenceFileIDs())
		responsesRouter.Use(middleware.TextgenParse(textgen.ProtocolOpenAIResponses))
		responsesRouter.Use(middleware.Distribute())
		responsesRouter.POST("/responses", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIResponses)
		})
	}
	{
		chatRouter := relayV1Router.Group("")
		chatRouter.Use(middleware.ModelRequestRateLimit())
		chatRouter.Use(middleware.TextgenParse(textgen.ProtocolOpenAIChat))
		chatRouter.Use(middleware.Distribute())
		chatRouter.POST("/chat/completions", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAI)
		})
	}
	{
		//http router
		// claude related routes: parsed once and planned like Chat and
		// Responses (middleware.ClaudeClientParse)
		messagesRouter := relayV1Router.Group("")
		messagesRouter.Use(middleware.ModelRequestRateLimit())
		messagesRouter.Use(middleware.ClaudeClientParse())
		messagesRouter.Use(middleware.Distribute())
		messagesRouter.POST("/messages", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatClaude)
		})
	}
	{
		httpRouter := relayV1Router.Group("")
		httpRouter.Use(middleware.ModelRequestRateLimit())
		httpRouter.Use(middleware.Distribute())

		// chat related routes
		httpRouter.POST("/completions", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAI)
		})
		// image related routes
		httpRouter.POST("/edits", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIImage)
		})
		httpRouter.POST("/images/generations", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIImage)
		})
		httpRouter.POST("/images/edits", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIImage)
		})

		// embedding related routes
		httpRouter.POST("/embeddings", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatEmbedding)
		})

		// audio related routes
		httpRouter.POST("/audio/transcriptions", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIAudio)
		})
		httpRouter.POST("/audio/translations", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIAudio)
		})
		httpRouter.POST("/audio/speech", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAIAudio)
		})

		// rerank related routes
		httpRouter.POST("/rerank", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatRerank)
		})

		// gemini relay routes
		httpRouter.POST("/engines/:model/embeddings", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatGemini)
		})
		httpRouter.POST("/models/*path", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatGemini)
		})

		// other relay routes
		httpRouter.POST("/moderations", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatOpenAI)
		})

		// not implemented
		httpRouter.POST("/images/variations", controller.RelayNotImplemented)
		httpRouter.POST("/fine-tunes", controller.RelayNotImplemented)
		httpRouter.GET("/fine-tunes", controller.RelayNotImplemented)
		httpRouter.GET("/fine-tunes/:id", controller.RelayNotImplemented)
		httpRouter.POST("/fine-tunes/:id/cancel", controller.RelayNotImplemented)
		httpRouter.GET("/fine-tunes/:id/events", controller.RelayNotImplemented)
		httpRouter.DELETE("/models/:model", controller.RelayNotImplemented)
	}

	relayGeminiRouter := router.Group("/v1beta")
	relayGeminiRouter.Use(middleware.SystemPerformanceCheck())
	relayGeminiRouter.Use(middleware.RelayRequestBodyLimit())
	relayGeminiRouter.Use(middleware.TokenAuth())
	relayGeminiRouter.Use(middleware.ModelRequestRateLimit())
	relayGeminiRouter.Use(middleware.Distribute())
	{
		// Gemini API 路径格式: /v1beta/models/{model_name}:{action}
		relayGeminiRouter.POST("/models/*path", func(c *gin.Context) {
			controller.Relay(c, types.RelayFormatGemini)
		})
	}
}
