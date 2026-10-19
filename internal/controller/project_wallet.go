package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
	"github.com/stripe/stripe-go/v81"
	"gorm.io/gorm"
)

// GetProjectTopUpDocument resolves a document against Stripe on each open, so
// emailed links keep working even when the originally stored URL changes.
func GetProjectTopUpDocument(c *gin.Context) {
	userID := c.GetInt("id")
	for _, selector := range c.QueryArray("project_id") {
		if selector != c.Param("id") {
			httperr.Code(c, types.ErrorCodeProjectContextMismatch)
			return
		}
	}
	var project model.Project
	if err := model.CoreDB().Where("public_id = ?", c.Param("id")).First(&project).Error; err != nil {
		projectFailure(c, notFoundAs(err, types.ErrorCodeProjectNotFound))
		return
	}
	var order model.TopUp
	if err := model.CoreDB().Where("project_id = ? AND public_id = ?", project.ID, c.Param("topup_id")).First(&order).Error; err != nil {
		projectFailure(c, notFoundAs(err, types.ErrorCodePaymentNotFound))
		return
	}
	// Owner and historical payer may open this order independently of project
	// visibility. Other readers still require an active manager membership.
	if order.UserId != userID && order.PaidByUserID != userID {
		var member model.ProjectMember
		if err := model.CoreDB().Where("project_id = ? AND user_id = ?", project.ID, userID).First(&member).Error; err != nil {
			projectFailure(c, notFoundAs(err, types.ErrorCodeProjectNotFound))
			return
		}
		if member.Status != model.ProjectMemberActive || !member.IsManager() {
			projectFailure(c, model.ErrProjectForbidden)
			return
		}
	}
	if order.Status != common.TopUpStatusSuccess || order.StripeCustomerID != project.StripeCustomerID {
		httperr.Code(c, types.ErrorCodeInvoiceUnavailable)
		return
	}
	api := projectStripeClient()
	var documentURL string
	var storedURL string
	switch c.Query("document") {
	case "invoice":
		if order.InvoiceStatus != "ready" || order.StripeInvoiceID == "" {
			httperr.Code(c, types.ErrorCodeInvoiceUnavailable)
			return
		}
		params := &stripe.InvoiceParams{}
		params.Context = c.Request.Context()
		invoice, err := api.Invoices.Get(order.StripeInvoiceID, params)
		if err != nil {
			storedURL = order.StripeInvoicePDF
			if !validStripeDocumentURL(storedURL) {
				httperr.Write(c, types.WrapErr(err, types.ErrorCodePaymentProviderUnavailable))
				return
			}
			documentURL = storedURL
			break
		}
		if invoice.Customer == nil || invoice.Customer.ID != order.StripeCustomerID || invoice.Metadata["lazu_top_up_reference"] != order.TradeNo || invoice.Status != stripe.InvoiceStatusPaid {
			httperr.Code(c, types.ErrorCodeInvoiceUnavailable)
			return
		}
		documentURL = invoice.InvoicePDF
	case "invoice_page":
		// Stripe renders the PDF of a Checkout-created (send_invoice) Invoice
		// with "Pay online" and bank-transfer instructions even after it is
		// paid; the hosted page is the surface that states the paid status,
		// and it offers both downloads itself.
		if order.InvoiceStatus != "ready" || order.StripeInvoiceID == "" {
			httperr.Code(c, types.ErrorCodeInvoiceUnavailable)
			return
		}
		params := &stripe.InvoiceParams{}
		params.Context = c.Request.Context()
		invoice, err := api.Invoices.Get(order.StripeInvoiceID, params)
		if err != nil {
			storedURL = order.StripeInvoiceURL
			if !validStripeDocumentURL(storedURL) {
				httperr.Write(c, types.WrapErr(err, types.ErrorCodePaymentProviderUnavailable))
				return
			}
			documentURL = storedURL
			break
		}
		if invoice.Customer == nil || invoice.Customer.ID != order.StripeCustomerID || invoice.Metadata["lazu_top_up_reference"] != order.TradeNo || invoice.Status != stripe.InvoiceStatusPaid {
			httperr.Code(c, types.ErrorCodeInvoiceUnavailable)
			return
		}
		documentURL = invoice.HostedInvoiceURL
	case "receipt":
		if order.PaidAmount <= 0 || order.StripePaymentIntentID == "" {
			httperr.Code(c, types.ErrorCodeInvoiceUnavailable)
			return
		}
		params := &stripe.PaymentIntentParams{}
		params.Context = c.Request.Context()
		params.AddExpand("latest_charge")
		intent, err := api.PaymentIntents.Get(order.StripePaymentIntentID, params)
		if err != nil {
			storedURL = order.StripeReceiptURL
			if !validStripeDocumentURL(storedURL) {
				httperr.Write(c, types.WrapErr(err, types.ErrorCodePaymentProviderUnavailable))
				return
			}
			documentURL = storedURL
			break
		}
		if intent.Customer == nil || intent.Customer.ID != order.StripeCustomerID || intent.Metadata["lazu_top_up_reference"] != order.TradeNo || intent.LatestCharge == nil || intent.LatestCharge.Customer == nil || intent.LatestCharge.Customer.ID != order.StripeCustomerID || intent.LatestCharge.PaymentIntent == nil || intent.LatestCharge.PaymentIntent.ID != intent.ID {
			httperr.Code(c, types.ErrorCodeInvoiceUnavailable)
			return
		}
		documentURL = intent.LatestCharge.ReceiptURL
	default:
		httperr.Invalid(c, "document")
		return
	}
	if !validStripeDocumentURL(documentURL) {
		httperr.Code(c, types.ErrorCodeInvoiceUnavailable)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"url": documentURL}})
}

// notFoundAs names a missing row after the thing the reader asked for.
func notFoundAs(err error, code types.ErrorCode) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return types.Err(code)
	}
	return err
}

func validStripeDocumentURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.Scheme == "https" && parsed.User == nil && parsed.Port() == "" &&
		(parsed.Hostname() == "pay.stripe.com" || parsed.Hostname() == "invoice.stripe.com")
}

func GetProjectWallet(c *gin.Context) {
	pc, ok := middleware.CurrentProject(c)
	if !ok || pc.Member.Status != model.ProjectMemberActive {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	var payer model.User
	if err := model.CoreDB().Select("quota").First(&payer, pc.Project.OwnerUserID).Error; err != nil {
		projectFailure(c, err)
		return
	}
	balance := model.ReadConsoleWalletBalance(c.Request.Context(), pc.Project.OwnerUserID, payer.Quota)
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	spend, err := model.ReadProjectSpend(pc.Project, start.Unix(), now.Unix()+1)
	if err != nil {
		projectFailure(c, err)
		return
	}
	daily := float64(spend) / float64(now.Day())
	var days *float64
	if daily > 0 {
		value := float64(max(balance, 0)) / daily
		days = &value
	}
	result := gin.H{"balance_microusd": balance, "days_left": days}
	if pc.Member.IsManager() {
		settings, err := pc.Project.GetSettings()
		if err != nil {
			projectFailure(c, err)
			return
		}
		var topups billing.MicroUSD
		if err := model.CoreDB().Model(&model.TopUp{}).Where("project_id = ? AND status = ? AND complete_time >= ? AND complete_time <= ?", pc.Project.ID, common.TopUpStatusSuccess, start.Unix(), now.Unix()).
			Select("COALESCE(SUM(CASE WHEN credited_microusd <> 0 THEN credited_microusd ELSE CAST(amount AS BIGINT) * 1000000 END),0)").Scan(&topups).Error; err != nil {
			projectFailure(c, err)
			return
		}
		details := pc.Project.BillingDetails
		if len(details) == 0 {
			details = model.JSONValue(`{"name":null,"address":null,"tax_ids":[]}`)
		}
		if c.Query("start_timestamp") != "" || c.Query("end_timestamp") != "" {
			from, fromErr := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
			to, toErr := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
			if fromErr != nil || toErr != nil || from <= 0 || to <= from || to-from > 32*86400 {
				httperr.Invalid(c, "start_timestamp")
				return
			}
			periodSpend, err := model.ReadProjectSpend(pc.Project, from, to)
			if err != nil {
				projectFailure(c, err)
				return
			}
			var periodTopups billing.MicroUSD
			if err := model.CoreDB().Model(&model.TopUp{}).Where("project_id = ? AND status = ? AND complete_time >= ? AND complete_time < ?", pc.Project.ID, common.TopUpStatusSuccess, from, to).Select("COALESCE(SUM(CASE WHEN credited_microusd <> 0 THEN credited_microusd ELSE CAST(amount AS BIGINT) * 1000000 END),0)").Scan(&periodTopups).Error; err != nil {
				projectFailure(c, err)
				return
			}
			result["period_spend_microusd"], result["period_topup_microusd"] = periodSpend, periodTopups
		}
		result["month_spend_microusd"] = spend
		result["month_topup_microusd"] = topups
		result["daily_avg_microusd"] = billing.MicroUSD(daily)
		result["project_cap"] = gin.H{"cap_microusd": settings.ProjectCapMicrousd, "spent_microusd": spend}
		result["billing_details"] = details
		result["capabilities"] = gin.H{"can_topup": model.CanProjectAction(pc.Member, nil, model.ProjectTopUp, settings), "can_manage_billing": pc.Member.Role == model.ProjectRoleOwner}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

func projectTopUpPayload(order model.TopUp) (gin.H, error) {
	var actor model.User
	if err := model.CoreDB().Unscoped().Select("public_id", "username").First(&actor, order.PaidByUserID).Error; err != nil {
		return nil, err
	}
	status := order.Status
	if status == common.TopUpStatusSuccess {
		status = "paid"
	}
	if status != "pending" && status != "paid" && status != "expired" {
		status = "failed"
	}
	invoiceStatus := order.InvoiceStatus
	if invoiceStatus == "" {
		invoiceStatus = "legacy"
	}
	var invoice interface{}
	if invoiceStatus == "ready" {
		invoice = gin.H{"number": order.StripeInvoiceNumber, "hosted_url": order.StripeInvoiceURL, "pdf_url": order.StripeInvoicePDF, "bill_to": order.BillTo}
	}
	var receipt interface{}
	if order.StripeReceiptURL != "" {
		receipt = order.StripeReceiptURL
	}
	credit := order.CreditedMicrousd
	if credit == 0 {
		credit = billing.USD(order.OrderUSD())
	}
	return gin.H{"id": order.PublicID, "created_at": order.CreateTime, "paid_amount_microusd": billing.USD(order.PaidAmount), "credited_microusd": credit,
		"paid_by": model.ProjectUserRef{ID: actor.PublicID, Username: actor.Username}, "method": order.PaymentMethod, "payment_status": status, "invoice_status": invoiceStatus, "invoice": invoice, "receipt_url": receipt}, nil
}

// GetOwnPaymentRecord is the account-wallet deep link for one payment. The
// credited account owner and the person who paid may read this one record,
// even after the payer has left the project. It does not grant project wallet
// access or return raw Stripe URLs.
func GetOwnPaymentRecord(c *gin.Context) {
	userID := c.GetInt("id")
	var order model.TopUp
	if err := model.CoreDB().Where("public_id = ? AND (user_id = ? OR paid_by_user_id = ?)", c.Param("topup_id"), userID, userID).First(&order).Error; err != nil {
		projectFailure(c, notFoundAs(err, types.ErrorCodePaymentNotFound))
		return
	}
	var project model.Project
	if err := model.CoreDB().Select("public_id", "name", "upgraded_at").First(&project, order.ProjectID).Error; err != nil {
		projectFailure(c, notFoundAs(err, types.ErrorCodePaymentNotFound))
		return
	}
	status := order.Status
	if status == common.TopUpStatusSuccess {
		status = "paid"
	}
	if status != "paid" && status != "pending" && status != "expired" {
		status = "failed"
	}
	credit := order.CreditedMicrousd
	if credit == 0 {
		credit = billing.USD(order.OrderUSD())
	}
	// A personal (default) project is an internal concept: the console names
	// the reader's own account in its own language, so only a shared project
	// carries a name here.
	projectName := ""
	projectShared := project.UpgradedAt != nil
	if order.StripeProjectShared != nil {
		projectShared = *order.StripeProjectShared
	}
	if projectShared {
		projectName = project.Name
		if order.StripeProjectName != "" {
			projectName = order.StripeProjectName
		}
	}
	var billTo any
	if len(order.BillTo) > 0 {
		billTo = json.RawMessage(order.BillTo)
	}
	invoiceStatus := order.InvoiceStatus
	if invoiceStatus == "" {
		invoiceStatus = "legacy"
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"id": order.PublicID, "project_id": project.PublicID, "project_name": projectName, "project_shared": projectShared,
		"paid_at": order.CompleteTime, "created_at": order.CreateTime,
		"order_amount_microusd": order.CreditedMicrousd,
		"paid_amount_microusd":  billing.USD(order.PaidAmount), "credited_microusd": credit, "bonus_microusd": order.BonusMicrousd,
		"payment_status": status, "payment_method": order.PaymentMethod,
		"invoice_status": invoiceStatus, "invoice_number": order.StripeInvoiceNumber, "bill_to": billTo,
		"invoice_ready": status == "paid" && order.InvoiceStatus == "ready" && order.StripeInvoiceID != "",
		"receipt_ready": status == "paid" && order.PaidAmount > 0 && order.StripePaymentIntentID != "" && validStripeDocumentURL(order.StripeReceiptURL),
	}})
}
func ListProjectTopUps(c *gin.Context) {
	pc, ok := middleware.CurrentProject(c)
	if !ok || !pc.Member.IsManager() {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	page, size, ok := projectPagination(c)
	if !ok {
		return
	}
	var count int64
	query := model.CoreDB().Model(&model.TopUp{}).Where("project_id = ?", pc.Project.ID)
	if err := query.Count(&count).Error; err != nil {
		projectFailure(c, err)
		return
	}
	var orders []model.TopUp
	if err := query.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&orders).Error; err != nil {
		projectFailure(c, err)
		return
	}
	items := make([]gin.H, 0, len(orders))
	for _, order := range orders {
		item, err := projectTopUpPayload(order)
		if err != nil {
			projectFailure(c, err)
			return
		}
		items = append(items, item)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"items": items, "total": count, "page": page, "page_size": size}})
}
func GetProjectTopUp(c *gin.Context) {
	pc, ok := middleware.CurrentProject(c)
	if !ok || !pc.Member.IsManager() {
		projectFailure(c, model.ErrProjectForbidden)
		return
	}
	var order model.TopUp
	if err := model.CoreDB().Where("project_id = ? AND public_id = ?", pc.Project.ID, c.Param("topup_id")).First(&order).Error; err != nil {
		projectFailure(c, notFoundAs(err, types.ErrorCodePaymentNotFound))
		return
	}
	item, err := projectTopUpPayload(order)
	if err != nil {
		projectFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": item})
}
