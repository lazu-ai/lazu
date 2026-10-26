package router

import (
	"github.com/lazu-ai/lazu/internal/controller"
	filecontroller "github.com/lazu-ai/lazu/internal/controller/files"
	"github.com/lazu-ai/lazu/internal/middleware"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

func SetApiRouter(router *gin.Engine) {
	apiRouter := router.Group("/api")
	apiRouter.Use(gzip.Gzip(gzip.DefaultCompression, gzip.WithExcludedPaths([]string{"/api/models/sync_openrouter", "/api/models/sync_litellm"})))
	apiRouter.Use(middleware.APIRateLimit())
	{
		apiRouter.GET("/status", controller.GetStatus)
		apiRouter.GET("/uptime/status", controller.GetUptimeKumaStatus)
		apiRouter.GET("/status/test", middleware.RootAuth(), controller.TestStatus)
		apiRouter.GET("/notice", controller.GetNotice)
		apiRouter.GET("/user-agreement", controller.GetUserAgreement)
		apiRouter.GET("/privacy-policy", controller.GetPrivacyPolicy)
		apiRouter.GET("/home_page_content", controller.GetHomePageContent)
		apiRouter.GET("/public/status", controller.GetPublicStatus)
		apiRouter.GET("/public/changelog", controller.GetPublicChangelog)
		apiRouter.GET("/public/changelog.md", controller.GetPublicChangelogMarkdown)
		apiRouter.POST("/public/token/inspect", middleware.LookupRateLimit(), controller.PublicInspectTokenUsage)
		apiRouter.GET("/pricing", middleware.TryUserAuth(), controller.GetPricing)
		apiRouter.GET("/pricing/sell_prices", middleware.RootAuth(), controller.GetModelSellPrices)
		apiRouter.PUT("/pricing/sell_prices", middleware.RootAuth(), controller.UpsertModelSellPrice)
		// Removing a price is a real operation, not something to do with SQL:
		// the handler refuses while anything still serves the model.
		apiRouter.DELETE("/pricing/sell_prices", middleware.RootAuth(), controller.DeleteModelSellPrice)
		apiRouter.GET("/pricing/wallet_reconciliation", middleware.RootAuth(), controller.GetWalletLedgerReconciliationMismatches)
		apiRouter.GET("/models/catalog", middleware.TokenAuth(), middleware.RejectEphemeralCredentialOutsideRelay(), controller.ListModelCatalog)
		credentialRoute := apiRouter.Group("/credentials")
		credentialRoute.Use(middleware.EphemeralCredentialRateLimit(), middleware.TokenAuth(), middleware.RejectEphemeralCredentialOutsideRelay())
		{
			credentialRoute.POST("/ephemeral", controller.IssueEphemeralCredential)
		}
		apiRouter.GET("/models/rankings", retiring(userSunset, ""), controller.GetModelRankings)
		apiRouter.GET("/community/models", retiring(userSunset, ""), controller.GetCommunityModels)
		apiRouter.GET("/community/models/:id", retiring(userSunset, ""), controller.GetCommunityModelByID)
		apiRouter.GET("/community/stats", retiring(userSunset, ""), controller.GetCommunityStats)
		apiRouter.GET("/v1/models/info", controller.GetPublicModelList)
		apiRouter.POST("/community/export", retiring(userSunset, ""), middleware.RootAuth(), controller.ExportCommunityModels)
		apiRouter.GET("/verification", middleware.MailSendRateLimit(), controller.SendEmailVerification)
		apiRouter.GET("/reset_password", middleware.MailSendRateLimit(), controller.SendPasswordResetEmail)
		apiRouter.POST("/user/reset", middleware.LoginRateLimit(), controller.ResetPassword)
		apiRouter.POST("/user/reset/v2", middleware.LoginRateLimit(), controller.ResetPasswordV2)
		apiRouter.POST("/stripe/webhook", controller.StripeWebhook)
		apiRouter.POST("/telegram/webhook", controller.TelegramWebhook)
		// Mail links: one-click opt-out (GET confirms, POST applies) and the
		// usage digest's chart image. Both are signed, so no session is needed.
		apiRouter.GET("/mail/unsubscribe", middleware.LookupRateLimit(), controller.MailUnsubscribePage)
		apiRouter.POST("/mail/unsubscribe", middleware.LookupRateLimit(), controller.MailUnsubscribeApply)
		apiRouter.GET("/mail/chart/:file", controller.MailDigestChart)
		apiRouter.GET("/auth/oidc/status", controller.OIDCStatus)
		apiRouter.GET("/auth/oidc/login", middleware.LoginRateLimit(), controller.OIDCLogin)
		apiRouter.GET("/auth/oidc/callback", middleware.LoginRateLimit(), controller.OIDCCallback)
		apiRouter.GET("/auth/oidc/bootstrap", middleware.LoginRateLimit(), controller.OIDCBootstrap)
		// Renders the identity email templates with sample data so they can be
		// reviewed in a browser without sending mail.
		apiRouter.GET("/auth/email-preview/:template", middleware.RootAuth(), controller.PreviewAuthEmail)
		// One email code, both doors: it signs an existing account in and
		// creates one for an address we have never seen.
		apiRouter.POST("/auth/email/start", middleware.MailSendRateLimit(), controller.StartEmailAuth)
		apiRouter.POST("/auth/email/verify", middleware.LoginRateLimit(), controller.VerifyEmailAuth)
		apiRouter.GET("/auth/oauth/:provider/login", middleware.LoginRateLimit(), controller.SocialOAuthLogin)
		apiRouter.GET("/auth/oauth/:provider/callback", middleware.LoginRateLimit(), controller.SocialOAuthCallback)

		// A personal wallet remains available when shared projects are disabled.
		// The resolver and document handler enforce membership and payer access.
		apiRouter.GET("/project/:id/topups/:topup_id/document", middleware.UserAuth(), controller.GetProjectTopUpDocument)
		projectRoute := apiRouter.Group("/project/:id", middleware.UserAuth(), middleware.ProjectConsoleContext())
		projectRoute.GET("", controller.GetProject)
		projectRoute.PUT("", controller.UpdateProject)
		projectRoute.POST("/dissolve", controller.DissolveProject)
		projectRoute.POST("/leave", controller.LeaveProject)
		projectRoute.GET("/invites", controller.ListProjectInvites)
		projectRoute.POST("/invites", controller.CreateProjectInvite)
		projectRoute.POST("/invites/:iid/resend", controller.ResendProjectInvite)
		projectRoute.POST("/invites/:iid/revoke", controller.RevokeProjectInvite)
		apiRouter.GET("/invite/:token", middleware.LookupRateLimit(), controller.PreviewProjectInvite)
		apiRouter.POST("/invite/:token/accept", middleware.UserAuth(), middleware.AccountActionRateLimit(), controller.AcceptProjectInvite)
		apiRouter.GET("/user/self/stripe/portal", middleware.UserAuth(), controller.ProjectStripePortal)
		projectRoute.GET("/tokens", controller.ListProjectTokens)
		projectRoute.GET("/attention", controller.GetProjectAttention)
		projectRoute.GET("/wallet", controller.GetProjectWallet)
		projectRoute.GET("/topups", controller.ListProjectTopUps)
		projectRoute.GET("/topups/:topup_id", controller.GetProjectTopUp)
		projectRoute.POST("/topups/:topup_id/cancel", controller.CancelProjectTopUp)
		// Auto-reload is the owner's: settings, the Stripe card-save page and
		// its confirmation, and the off switch. The charge itself runs in the
		// worker, never on a request.
		projectRoute.GET("/auto-reload", controller.GetProjectAutoReload)
		projectRoute.PUT("/auto-reload", controller.UpdateProjectAutoReload)
		projectRoute.POST("/auto-reload/setup", middleware.AccountActionRateLimit(), controller.StartProjectAutoReloadSetup)
		projectRoute.POST("/auto-reload/confirm", middleware.AccountActionRateLimit(), controller.ConfirmProjectAutoReloadSetup)
		projectRoute.POST("/auto-reload/disable", controller.DisableProjectAutoReload)
		projectRoute.GET("/my-budget", controller.GetProjectMyBudget)
		projectRoute.GET("/members/:uid/budget-history", controller.GetProjectBudgetHistory)
		projectRoute.GET("/budget-requests", controller.ListProjectBudgetRequests)
		projectRoute.POST("/budget-requests", controller.CreateProjectBudgetRequest)
		projectRoute.POST("/budget-requests/:rid/approve", controller.ApproveProjectBudgetRequest)
		projectRoute.POST("/budget-requests/:rid/reject", controller.RejectProjectBudgetRequest)
		projectRoute.POST("/budget-requests/:rid/withdraw", controller.WithdrawProjectBudgetRequest)
		projectRoute.GET("/members", controller.ListProjectMembers)
		projectRoute.GET("/members/:uid", controller.GetProjectMember)
		projectRoute.PUT("/members/:uid/role", controller.UpdateProjectMemberRole)
		projectRoute.PUT("/members/:uid/status", controller.UpdateProjectMemberStatus)
		projectRoute.PUT("/members/:uid/budget", controller.UpdateProjectMemberBudget)
		projectRoute.DELETE("/members/:uid", controller.RemoveProjectMember)
		projectRoute.POST("/members/:uid/undo-remove", controller.UndoProjectMemberRemoval)

		userRoute := apiRouter.Group("/user")
		{
			userRoute.POST("/register", middleware.RegisterRateLimit(), controller.Register)
			userRoute.POST("/login", middleware.LoginRateLimit(), controller.Login)
			userRoute.GET("/logout", controller.Logout)

			selfRoute := userRoute.Group("/")
			selfRoute.Use(middleware.UserAuth())
			{
				selfRoute.GET("/self/onboarding", controller.GetSelfOnboarding)
				selfRoute.POST("/self/onboarding/dismiss", controller.DismissSelfOnboarding)
				selfRoute.GET("/self/identities", controller.GetSelfIdentities)
				selfRoute.GET("/self/username/check", middleware.UsernameCheckRateLimit(), controller.CheckSelfUsername)
				selfRoute.DELETE("/self/identities/:provider", controller.DeleteSelfIdentity)
				selfRoute.GET("/self", controller.GetSelf)
				selfRoute.GET("/models", controller.GetUserModels)
				selfRoute.GET("/models/catalog", controller.ListUserModelCatalog)
				selfRoute.GET("/models/classified", retiring(userSunset, "/api/user/models/catalog"), controller.GetUserModelsClassified)
				selfRoute.PUT("/self", controller.UpdateSelf)
				selfRoute.PUT("/self/language", controller.UpdateSelfLanguage)
				selfRoute.PUT("/self/timezone", controller.UpdateSelfTimezone)
				selfRoute.PUT("/self/mail-prefs", controller.UpdateSelfMailPrefs)
				selfRoute.GET("/self/digest-preview", controller.GetSelfDigestPreview)
				selfRoute.DELETE("/self", controller.DeleteSelf)
				selfRoute.GET("/token", controller.GenerateAccessToken)
				selfRoute.GET("/topup/info", controller.GetTopUpInfo)
				selfRoute.GET("/topup/self", controller.GetUserTopUps)
				selfRoute.GET("/payments/:topup_id", controller.GetOwnPaymentRecord)
				selfRoute.GET("/wallet/ledger", controller.GetUserWalletLedger)
				selfRoute.POST("/stripe/pay", middleware.AccountActionRateLimit(), controller.RequestStripePay)
				selfRoute.POST("/self/stripe/pay", retiring(userSunset, "/api/user/stripe/pay"), middleware.AccountActionRateLimit(), controller.RequestStripePay)
				selfRoute.POST("/stripe/amount", retiring(userSunset, ""), controller.RequestStripeAmount)
				selfRoute.PUT("/setting", controller.UpdateUserSetting)
				selfRoute.GET("/telegram", controller.GetSelfTelegram)
				selfRoute.POST("/telegram/link", middleware.AccountActionRateLimit(), controller.CreateSelfTelegramLink)
				selfRoute.DELETE("/telegram", controller.DeleteSelfTelegram)
				selfRoute.GET("/used-models", retiring(userSunset, ""), controller.GetUserUsedModels)
				selfRoute.GET("/email/bind", middleware.AccountActionRateLimit(), controller.EmailBind)
				selfRoute.POST("/support/report", middleware.AccountActionRateLimit(), controller.SubmitSupportReport)

			}

			adminRoute := userRoute.Group("/")
			adminRoute.Use(middleware.RootAuth())
			{
				adminRoute.GET("/", controller.GetAllUsers)
				adminRoute.GET("/topup", controller.GetAllTopUps)
				adminRoute.GET("/payment-disputes", controller.AdminListPaymentDisputes)
				adminRoute.POST("/payment-disputes/:topup_id/resolve", controller.AdminResolvePaymentDispute)
				adminRoute.POST("/payments/:topup_id/reconcile-documents", controller.AdminReconcilePaymentDocument)
				adminRoute.POST("/topup/complete", controller.AdminCompleteTopUp)
				adminRoute.GET("/search", controller.SearchUsers)
				adminRoute.GET("/summary", controller.GetUserSummary)
				adminRoute.GET("/lookup", controller.LookupCustomerAccount)
				adminRoute.GET("/:id", controller.GetUser)
				adminRoute.GET("/:id/summary", controller.GetUserAccountSummary)
				adminRoute.GET("/:id/ledger", controller.GetUserWalletLedgerEvents)
				adminRoute.GET("/:id/tokens", controller.GetUserTokens)
				adminRoute.GET("/:id/dashboard", controller.GetUserAccountDashboard)
				adminRoute.POST("/", controller.CreateUser)
				adminRoute.POST("/manage", controller.ManageUser)
				adminRoute.POST("/:id/quota/adjust", controller.AdjustUserQuota)
				adminRoute.PUT("/", controller.UpdateUser)
				adminRoute.DELETE("/:id", controller.DeleteUser)

			}
			rootRoute := userRoute.Group("/")
			rootRoute.Use(middleware.RootAuth())
			{
				rootRoute.PUT("/:id/has_topped_up", retiring(userSunset, ""), controller.SetUserHasToppedUp)
			}
		}
		accessAddressRoute := apiRouter.Group("/access-address")
		accessAddressRoute.Use(middleware.RootAuth())
		{
			accessAddressRoute.GET("/", controller.GetAccessAddresses)
			accessAddressRoute.PUT("/", controller.UpdateAccessAddresses)
			accessAddressRoute.PUT("/grant", controller.SetAccessAddressGrant)
		}
		optionRoute := apiRouter.Group("/option")
		optionRoute.Use(middleware.RootAuth())
		{
			optionRoute.GET("/", controller.GetOptions)
			optionRoute.PUT("/", controller.UpdateOption)
		}
		performanceRoute := apiRouter.Group("/performance")
		performanceRoute.Use(middleware.RootAuth())
		{
			performanceRoute.GET("/stats", controller.GetPerformanceStats)
		}
		announcementRoute := apiRouter.Group("/announcements")
		announcementRoute.Use(middleware.UserAuth())
		{
			announcementRoute.GET("", controller.ListAnnouncements)
			announcementRoute.GET("/", func(c *gin.Context) {
				markDeprecatedAlias(c, "/api/announcements")
				controller.ListAnnouncements(c)
			})
			announcementRoute.GET("/unseen", controller.ListUnseenAnnouncements)
			announcementRoute.POST("/:id/dismiss", controller.DismissAnnouncement)
		}
		adminConfigRoute := apiRouter.Group("/admin")
		adminConfigRoute.Use(middleware.RootAuth())
		{
			adminConfigRoute.GET("/deployment-status", controller.GetDeploymentStatus)
			adminConfigRoute.POST("/email/test", controller.SendTestEmail)
			adminConfigRoute.POST("/telegram/test", controller.SendTelegramTest)
			adminConfigRoute.POST("/telegram/mute", controller.MuteTelegramOps)
			adminConfigRoute.GET("/announcements", controller.AdminListAnnouncements)
			adminConfigRoute.POST("/announcements", controller.AdminCreateAnnouncement)
			adminConfigRoute.PUT("/announcements/:id", controller.AdminUpdateAnnouncement)
			adminConfigRoute.POST("/announcements/:id/unpublish", controller.AdminUnpublishAnnouncement)
		}
		// 运营 (root only).
		opsRoute := apiRouter.Group("/ops")
		opsRoute.Use(middleware.RootAuth())
		{
			opsRoute.GET("/gateway-performance", controller.AdminGatewayPerformance)
			opsRoute.GET("/mail/deliveries", controller.AdminListMailDeliveries)
			opsRoute.GET("/mail/deliveries/:id", controller.AdminGetMailDelivery)
			opsRoute.POST("/mail/deliveries/:id/resend", controller.AdminResendMailDelivery)
			opsRoute.POST("/mail/deliveries/:id/copy", controller.AdminCopyMailDelivery)
			opsRoute.POST("/mail/deliveries/:id/regenerate", controller.AdminRegenerateMailDelivery)
			opsRoute.GET("/mail/digest", controller.AdminGetDigestOverview)
			opsRoute.PUT("/mail/digest", controller.AdminSetDigest)
			opsRoute.GET("/mail/digest/preview", controller.AdminPreviewDigest)
			opsRoute.GET("/mail/digest/drafts", controller.AdminListDigestDrafts)
			opsRoute.POST("/mail/digest/drafts", controller.AdminGenerateDigestDrafts)
			opsRoute.POST("/mail/digest/copy", controller.AdminCopyDigest)
		}
		adminSpendBudgetRoute := apiRouter.Group("/admin/spend-budgets")
		adminSpendBudgetRoute.Use(middleware.RootAuth())
		{
			adminSpendBudgetRoute.GET("", controller.ListSpendBudgets)
			adminSpendBudgetRoute.POST("", controller.SaveSpendBudget)
			adminSpendBudgetRoute.GET("/alerts", controller.ListSpendBudgetAlerts)
			adminSpendBudgetRoute.PUT("/:id", controller.SaveSpendBudget)
			adminSpendBudgetRoute.DELETE("/:id", controller.DeleteSpendBudget)
		}
		adminHealthRoute := apiRouter.Group("/admin/health")
		adminHealthRoute.Use(middleware.RootAuth())
		{
			adminHealthRoute.GET("", controller.GetAdminHealth)
		}
		// Channel health is user-facing: a workspace member sees the lanes
		// their own models run on. The handler widens the projection for
		// admins rather than there being a second route, so both audiences
		// read the same aggregate.
		channelHealthRoute := apiRouter.Group("/channel_health")
		channelHealthRoute.Use(middleware.UserOrTokenAuth())
		{
			channelHealthRoute.GET("", controller.GetChannelHealth)
			// Supply decisions are operator-only: a member reading this page
			// sees lane status, never the pool behind it, let alone its shape.
			channelHealthRoute.PUT("/route_role", middleware.RootAuth(), controller.UpdateChannelHealthRouteRole)
			channelHealthRoute.POST("/route_retreat/clear", middleware.RootAuth(), controller.ClearChannelHealthRouteRetreat)
		}
		searchBackendRoute := apiRouter.Group("/search/backends")
		searchBackendRoute.Use(middleware.RootAuth())
		{
			searchBackendRoute.GET("", controller.ListSearchBackends)
			searchBackendRoute.POST("/:id/reveal", middleware.SecretRevealRateLimit(), controller.RevealSearchBackendKey)
			searchBackendRoute.POST("", controller.CreateSearchBackend)
			searchBackendRoute.PUT("/:id", controller.UpdateSearchBackend)
			searchBackendRoute.DELETE("/:id", controller.DeleteSearchBackend)
		}
		notificationRoute := apiRouter.Group("/notification")
		notificationRoute.Use(retiring(adminSunset, "/api/admin/announcements"), middleware.RootAuth())
		{
			notificationRoute.POST("/email/send", controller.SendBroadcastEmail)
			notificationRoute.POST("/email/announcement", controller.SendAnnouncementEmail)
		}
		channelRoute := apiRouter.Group("/channel")
		channelRoute.Use(middleware.RootAuth())
		{
			channelRoute.GET("/", controller.GetAllChannels)
			channelRoute.GET("/search", controller.SearchChannels)
			channelRoute.GET("/models", controller.ChannelListModels)
			channelRoute.GET("/models_enabled", controller.EnabledListModels)
			channelRoute.GET("/:id", controller.GetChannel)
			channelRoute.POST("/:id/key", middleware.SecretRevealRateLimit(), middleware.DisableCache(), controller.GetChannelKey)
			channelRoute.GET("/test", controller.TestAllChannels)
			channelRoute.GET("/test/:id", controller.TestChannel)
			channelRoute.POST("/:id/probe", controller.ProbeChannelForHealth)
			channelRoute.GET("/update_balance", controller.UpdateAllChannelsBalance)
			channelRoute.GET("/update_balance/:id", controller.UpdateChannelBalance)
			channelRoute.POST("/", controller.AddChannel)
			channelRoute.PUT("/", controller.UpdateChannel)
			channelRoute.PUT("/:id/status", controller.UpdateChannelStatusById)
			channelRoute.DELETE("/disabled", controller.DeleteDisabledChannel)
			channelRoute.POST("/tag/disabled", retiring(adminSunset, ""), controller.DisableTagChannels)
			channelRoute.POST("/tag/enabled", retiring(adminSunset, ""), controller.EnableTagChannels)
			channelRoute.PUT("/tag", retiring(adminSunset, ""), controller.EditTagChannels)
			channelRoute.DELETE("/:id", controller.DeleteChannel)
			channelRoute.POST("/batch", retiring(adminSunset, ""), controller.DeleteChannelBatch)
			channelRoute.POST("/fix", controller.FixChannelsAbilities)
			channelRoute.GET("/fetch_models/:id", controller.FetchUpstreamModels)
			channelRoute.POST("/fetch_models", controller.FetchModels)
			channelRoute.POST("/batch/tag", retiring(adminSunset, ""), controller.BatchSetChannelTag)
			channelRoute.GET("/tag/models", retiring(adminSunset, ""), controller.GetTagModels)
			channelRoute.POST("/copy/:id", controller.CopyChannel)
			channelRoute.POST("/multi_key/manage", controller.ManageMultiKeys)
		}
		tokenRoute := apiRouter.Group("/token")
		tokenRoute.Use(middleware.UserAuth())
		{
			tokenRoute.GET("/options", controller.GetTokenOptions)
			tokenRoute.GET("/", controller.GetAllTokens)
			tokenRoute.GET("/search", controller.SearchTokens)
			tokenRoute.POST("/inspect", controller.InspectTokenUsage)
			tokenRoute.GET("/:id", controller.GetToken)
			tokenRoute.POST("/:id/reveal", middleware.SecretRevealRateLimit(), controller.RevealTokenKey)
			tokenRoute.GET("/:id/secret", middleware.SecretRevealRateLimit(), controller.RevealTokenKey)
			tokenRoute.POST("/:id/rotate", middleware.SecretRevealRateLimit(), controller.RotateProjectToken)
			tokenRoute.POST("/", controller.AddToken)
			tokenRoute.PUT("/", controller.UpdateToken)
			tokenRoute.DELETE("/:id", controller.DeleteToken)
			tokenRoute.POST("/batch", controller.DeleteTokenBatch)
		}

		filesRoute := apiRouter.Group("/files")
		filesRoute.Use(middleware.UserAuth())
		{
			filesRoute.GET("", filecontroller.ConsoleList)
			filesRoute.POST("", filecontroller.ConsoleUpload)
			filesRoute.DELETE("/:id", filecontroller.ConsoleDelete)
			filesRoute.GET("/:id/content", filecontroller.ConsoleContent)
		}

		usageRoute := apiRouter.Group("/usage")
		{
			tokenUsageRoute := usageRoute.Group("/token")
			tokenUsageRoute.Use(retiring(userSunset, "/api/usage/summary"), middleware.TokenAuth(), middleware.RejectEphemeralCredentialOutsideRelay())
			{
				tokenUsageRoute.GET("/", controller.GetTokenUsage)
			}
			usageRoute.GET("/summary", middleware.TokenAuth(), middleware.RejectEphemeralCredentialOutsideRelay(), controller.GetUsageSummary)
			usageRoute.GET("/requests/:request_id", middleware.TokenAuth(), middleware.RejectEphemeralCredentialOutsideRelay(), controller.GetUsageRequestDetail)
		}

		logRoute := apiRouter.Group("/log")
		logRoute.GET("/", middleware.RootAuth(), controller.GetAllLogs)
		logRoute.GET("/tasks", middleware.RootAuth(), controller.GetAllTasks)
		logRoute.DELETE("/", middleware.RootAuth(), controller.DeleteHistoryLogs)
		logRoute.GET("/stat", middleware.RootAuth(), controller.GetLogsStat)
		logRoute.GET("/self/stat", middleware.UserAuth(), controller.GetLogsSelfStat)
		logRoute.GET("/sessions", middleware.RootAuth(), controller.GetAllLogSessions)
		logRoute.GET("/sessions/:session_id", middleware.RootAuth(), controller.GetAllLogSessionDetail)
		logRoute.GET("/search", middleware.RootAuth(), controller.SearchAllLogs)
		logRoute.GET("/self", middleware.UserAuth(), controller.GetUserLogs)
		logRoute.GET("/self/in_flight", retiring(userSunset, "/api/log/self/tasks"), middleware.UserAuth(), controller.GetUserInFlightMediaJobs)
		logRoute.GET("/self/tasks", middleware.UserAuth(), controller.GetUserTasks)
		logRoute.GET("/self/tasks/:id", retiring(userSunset, ""), middleware.UserAuth(), controller.GetUserTask)
		logRoute.GET("/self/sessions", middleware.UserAuth(), controller.GetUserLogSessions)
		logRoute.GET("/self/sessions/:session_id", middleware.UserAuth(), controller.GetUserLogSessionDetail)
		logRoute.GET("/self/search", middleware.UserAuth(), controller.SearchUserLogs)

		dataRoute := apiRouter.Group("/data")
		dataRoute.GET("/", retiring(adminSunset, ""), middleware.RootAuth(), controller.GetAllUsageData)
		dataRoute.GET("/self", retiring(userSunset, "/api/data/dashboard/self"), middleware.UserAuth(), controller.GetUserUsageData)
		dataRoute.GET("/dashboard", middleware.UserAuth(), controller.GetUserDashboardUsage)
		dataRoute.GET("/overview", middleware.UserAuth(), controller.GetOverview)
		dataRoute.GET("/dashboard/self", middleware.UserAuth(), controller.GetSelfDashboardUsage)
		dataRoute.GET("/export", middleware.UserAuth(), controller.ExportUserUsage)

		logRoute.GET("/token", retiring(userSunset, "/api/log/self"), middleware.UserAuth(), controller.GetLogByKey)
		groupRoute := apiRouter.Group("/group")
		groupRoute.Use(middleware.RootAuth())
		{
			groupRoute.GET("/", controller.GetGroups)
		}

		// 专属折扣: negotiated discounts off the official price that a set of
		// customers share (see model.PricePlan).
		pricePlanRoute := apiRouter.Group("/price-plans")
		pricePlanRoute.Use(middleware.RootAuth())
		{
			pricePlanRoute.GET("", controller.ListPricePlans)
			pricePlanRoute.POST("", controller.CreatePricePlan)
			pricePlanRoute.GET("/:id", controller.GetPricePlan)
			pricePlanRoute.PUT("/:id", controller.UpdatePricePlan)
			pricePlanRoute.DELETE("/:id", controller.DeletePricePlan)
			pricePlanRoute.POST("/:id/members", controller.AddPricePlanMember)
			pricePlanRoute.DELETE("/:id/members/:user", controller.RemovePricePlanMember)
		}

		// Model ranking admin controls
		modelRankingAdmin := apiRouter.Group("/models/rankings")
		modelRankingAdmin.Use(retiring(userSunset, ""), middleware.RootAuth())
		{
			modelRankingAdmin.POST("/recalculate", controller.RecalculateModelRankings)
		}

		// Monthly reports
		reportRoute := apiRouter.Group("/reports")
		reportRoute.Use(middleware.UserAuth())
		{
			reportRoute.GET("/monthly", controller.ListMonthlyReports)
			reportRoute.GET("/monthly/latest", controller.GetMonthlyReportLatest)
			reportRoute.POST("/monthly/generate", controller.GenerateMonthlyReport)
			reportRoute.GET("/monthly/subscription", controller.GetMonthlyReportSubscription)
			reportRoute.PUT("/monthly/subscription", controller.UpdateMonthlyReportSubscription)
		}

		prefillGroupRoute := apiRouter.Group("/prefill_group")
		prefillGroupRoute.Use(middleware.RootAuth())
		{
			prefillGroupRoute.GET("/", controller.GetPrefillGroups)
			prefillGroupRoute.POST("/", controller.CreatePrefillGroup)
			prefillGroupRoute.PUT("/", controller.UpdatePrefillGroup)
			prefillGroupRoute.DELETE("/:id", controller.DeletePrefillGroup)
		}

		vendorRoute := apiRouter.Group("/vendors")
		vendorRoute.Use(middleware.RootAuth())
		{
			vendorRoute.GET("/", controller.GetAllVendors)
			vendorRoute.GET("/search", controller.SearchVendors)
			vendorRoute.GET("/:id", controller.GetVendorMeta)
			vendorRoute.POST("/", controller.CreateVendorMeta)
			vendorRoute.PUT("/", controller.UpdateVendorMeta)
			vendorRoute.DELETE("/:id", controller.DeleteVendorMeta)
		}

		modelsRoute := apiRouter.Group("/models")
		modelsRoute.Use(middleware.RootAuth())
		{
			modelsRoute.GET("/sync_upstream/preview", controller.SyncUpstreamPreview)
			modelsRoute.POST("/sync_upstream", controller.SyncUpstreamModels)
			modelsRoute.GET("/sync_openrouter/preview", controller.SyncOpenRouterPreview)
			modelsRoute.POST("/sync_openrouter", controller.SyncOpenRouter)
			modelsRoute.GET("/sync_litellm/preview", controller.SyncLiteLLMPreview)
			modelsRoute.POST("/sync_litellm", controller.SyncLiteLLM)
			modelsRoute.GET("/missing", controller.GetMissingModels)
			modelsRoute.GET("/", controller.GetAllModelsMeta)
			modelsRoute.GET("/search", controller.SearchModelsMeta)
			modelsRoute.GET("/:id", controller.GetModelMeta)
			modelsRoute.POST("/", controller.CreateModelMeta)
			modelsRoute.PUT("/", controller.UpdateModelMeta)
			modelsRoute.DELETE("/:id", controller.DeleteModelMeta)
		}

		// 模型运营 console. Joins the layers the two tabs need (模型 × 对外档位 ×
		// 来源池 on one side, the 来源 ledger on the other) so the page makes a
		// bounded number of requests instead of fanning out per row.
		modelOpsRoute := apiRouter.Group("/model_ops")
		modelOpsRoute.Use(middleware.RootAuth())
		{
			modelOpsRoute.GET("/sources", controller.GetModelOpsSources)
			modelOpsRoute.GET("/pools", controller.GetModelOpsPools)
			modelOpsRoute.GET("/spend", controller.GetModelOpsSpend)
			modelOpsRoute.PUT("/pool_weight", controller.UpdateModelOpsPoolWeight)
			modelOpsRoute.PUT("/pool_weight/batch", controller.BatchUpdateModelOpsPoolWeight)
			modelOpsRoute.PUT("/models/tier_config", controller.SaveModelOpsTierConfig)
			modelOpsRoute.PUT("/models/status", controller.BatchUpdateModelOpsStatus)
			modelOpsRoute.POST("/sources/:id/go_live", controller.GoLiveModelOpsSource)
			modelOpsRoute.GET("/sources/:id/observation", controller.GetModelOpsSourceObservation)
			modelOpsRoute.POST("/sources/:id/remove_models", controller.RemoveModelOpsSourceModels)
			// 档位 are edited from 模型运营, alongside the prices they carry.
			modelOpsRoute.GET("/lanes", controller.GetChannelLanes)
			modelOpsRoute.POST("/lanes", middleware.RootAuth(), controller.SaveChannelLane)
			modelOpsRoute.PUT("/lanes/:slug", middleware.RootAuth(), controller.SaveChannelLane)
			modelOpsRoute.PUT("/lanes/:slug/enabled", middleware.RootAuth(), controller.SetChannelLaneEnabled)
			modelOpsRoute.DELETE("/lanes/:slug", middleware.RootAuth(), controller.DeleteChannelLane)
		}

		// 资料更新: model facts from models.dev, the review queue, and the
		// maker order every catalog reads.
		modelInfoRoute := apiRouter.Group("/model_info")
		modelInfoRoute.Use(middleware.RootAuth())
		{
			modelInfoRoute.GET("/reviews", controller.GetModelInfoReviews)
			modelInfoRoute.GET("/reviews/count", controller.GetModelInfoReviewCount)
			modelInfoRoute.POST("/reviews/first_run/accept", controller.AcceptAllModelInfoFirstRun)
			modelInfoRoute.POST("/reviews/:id/:action", controller.ResolveModelInfoReview)
			modelInfoRoute.POST("/sync", controller.RunModelInfoSyncNow)
			modelInfoRoute.GET("/lookup", controller.LookupModelInfo)
			modelInfoRoute.GET("/makers", controller.GetModelMakers)
			modelInfoRoute.PUT("/makers", controller.UpdateModelMakerOrder)
			modelInfoRoute.PUT("/model", controller.UpdateModelInfo)
		}

		// Manual upload trigger (Admin)
		uploadRoute := apiRouter.Group("/upload")
		uploadRoute.Use(retiring(adminSunset, ""), middleware.RootAuth())
		{
			uploadRoute.POST("/run", controller.UploadNow)
			uploadRoute.GET("/pending-files", controller.GetPendingFiles)
			uploadRoute.POST("/single", controller.UploadSingleFile)
		}

		// Manual worker triggers (Admin)
		workersRoute := apiRouter.Group("/workers")
		workersRoute.Use(retiring(adminSunset, ""), middleware.RootAuth())
		{
			workersRoute.GET("/status", controller.GetWorkerStatuses)
			workersRoute.GET("/status/:name", controller.GetWorkerStatus)
			workersRoute.POST("/channel-test", controller.TriggerChannelTest)
		}

		for _, register := range apiRoutes {
			register(apiRouter)
		}
	}
}
