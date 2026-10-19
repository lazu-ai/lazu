package model

import (
	"net/url"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"gorm.io/gorm"
)

// QueuePaymentDocumentAlerts queues one administrator notice per order and reason.
// It includes older orders so a failed invoice cannot disappear after the
// 30-day Stripe reconciliation window.
func QueuePaymentDocumentAlerts(now time.Time, adminEmail string) error {
	adminEmail = strings.TrimSpace(adminEmail)
	if adminEmail == "" {
		return nil
	}
	cutoff := now.Add(-time.Hour).Unix()
	const reasonSQL = "CASE WHEN top_ups.invoice_status = 'failed' THEN 'invoice_finalization_failed' WHEN top_ups.invoice_status = 'ready' AND top_ups.stripe_invoice_pdf <> '' THEN 'receipt_missing_after_one_hour' ELSE 'invoice_missing_after_one_hour' END"
	var orders []TopUp
	if err := CoreDB().Model(&TopUp{}).
		Where("top_ups.project_id > 0 AND top_ups.status = ? AND top_ups.complete_time > 0 AND top_ups.complete_time <= ? AND top_ups.invoice_status <> ? AND (top_ups.invoice_status <> ? OR top_ups.stripe_invoice_pdf = '' OR (top_ups.paid_amount > 0 AND top_ups.stripe_receipt_url = ''))", common.TopUpStatusSuccess, cutoff, "legacy", "ready").
		Where("NOT EXISTS (SELECT 1 FROM project_notifications AS alerts WHERE alerts.event_key = 'payment-alert:' || top_ups.public_id || ':' || " + reasonSQL + ")").
		Order("top_ups.id ASC").Limit(200).Find(&orders).Error; err != nil {
		return err
	}
	for _, order := range orders {
		reason := "invoice_missing_after_one_hour"
		if order.InvoiceStatus == "failed" {
			reason = "invoice_finalization_failed"
		} else if order.InvoiceStatus == "ready" && order.StripeInvoicePDF != "" {
			reason = "receipt_missing_after_one_hour"
		}
		if err := queuePaymentAdminAlert(order, reason, adminEmail); err != nil {
			return err
		}
	}
	var failedEmailOrders []TopUp
	if err := CoreDB().Model(&ProjectNotification{}).Select("top_ups.*").
		Joins("JOIN top_ups ON (project_notifications.event_key LIKE ('payment:' || top_ups.public_id || ':%') OR project_notifications.event_key LIKE ('invoice:' || top_ups.public_id || ':%'))").
		Where("project_notifications.status = ? AND project_notifications.first_failed_at IS NOT NULL AND project_notifications.first_failed_at <= ?", "pending", cutoff).
		Where("NOT EXISTS (SELECT 1 FROM project_notifications AS alerts WHERE alerts.event_key = 'payment-alert:' || top_ups.public_id || ':document_email_failed')").
		Order("project_notifications.id ASC").Limit(200).Scan(&failedEmailOrders).Error; err != nil {
		return err
	}
	for _, order := range failedEmailOrders {
		if err := queuePaymentAdminAlert(order, "document_email_failed", adminEmail); err != nil {
			return err
		}
	}
	return nil
}

func QueueProjectInvoiceFailureAlert(reference, adminEmail, stripeError string) error {
	if strings.TrimSpace(adminEmail) == "" {
		return nil
	}
	var order TopUp
	if err := CoreDB().Where("trade_no = ? AND project_id > 0", reference).First(&order).Error; err != nil {
		return err
	}
	if order.InvoiceStatus != "failed" {
		return nil
	}
	return queuePaymentAdminAlertWithDetail(order, "invoice_finalization_failed", strings.TrimSpace(adminEmail), stripeError)
}

func queuePaymentAdminAlert(order TopUp, reason, adminEmail string) error {
	return queuePaymentAdminAlertWithDetail(order, reason, adminEmail, "")
}

func queuePaymentAdminAlertWithDetail(order TopUp, reason, adminEmail, detail string) error {
	return CoreDB().Transaction(func(tx *gorm.DB) error {
		key := "payment-alert:" + order.PublicID + ":" + reason
		title := "付款单据需要处理 · " + order.PublicID
		blocks := []common.MailBlock{{Facts: []common.MailFact{
			{Label: "充值", Value: order.PublicID},
			{Label: "Invoice", Value: order.StripeInvoiceID},
			{Label: "原因", Value: reason},
		}}}
		if detail = strings.TrimSpace(detail); detail != "" {
			blocks = append(blocks, common.MailBlock{Snippet: "Stripe: " + detail})
		}
		if order.StripeInvoiceID != "" {
			blocks = append(blocks, common.MailBlock{Primary: &common.MailAction{Label: "在 Stripe 打开", URL: "https://dashboard.stripe.com/invoices/" + url.PathEscape(order.StripeInvoiceID)}})
		}
		body, err := common.RenderMail(common.Mail{Tier: common.MailOps, Source: "PAYMENT", Severity: "WARN", Title: title, Preheader: reason, Blocks: blocks})
		if err != nil {
			return err
		}
		return QueueProjectEmailTx(tx, order.ProjectID, 0, key, ProjectEmail{Receiver: adminEmail, Subject: "[Lazu ops] " + title, HTML: body})
	})
}
