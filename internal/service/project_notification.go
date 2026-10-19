package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

// DeliverProjectNotifications is shared by the immediate API wakeup and the
// periodic worker. Claiming prevents two live dispatchers sending one event.
func DeliverProjectNotifications(ctx context.Context, now time.Time) error {
	return deliverProjectNotifications(ctx, now, func(row model.ProjectNotification, subject string, receiver string, html string) error {
		return common.SendMail(projectNotificationMailMeta(row), subject, receiver, html)
	})
}

// projectNotificationMailMeta files a team or payment mail in the archive.
// Invitations carry a join link, so only their subject is kept.
func projectNotificationMailMeta(row model.ProjectNotification) common.MailMeta {
	prefix, _, _ := strings.Cut(row.EventKey, ":")
	meta := common.MailMeta{Kind: common.MailKindTeam, UserID: row.RecipientUserID}
	switch prefix {
	case "invite":
		meta.Sensitive = true
	case "payment", "invoice", "topup", "receipt":
		meta.Kind = common.MailKindPayment
	case "payment-alert":
		meta.Kind = common.MailKindOps
	}
	return meta
}

func deliverProjectNotifications(ctx context.Context, now time.Time, send func(model.ProjectNotification, string, string, string) error) error {
	if model.CoreDB() == nil {
		return nil
	}
	var rows []model.ProjectNotification
	if err := model.CoreDB().Where("(status = ? AND available_at <= ?) OR (status = ? AND claimed_at < ?)", "pending", now.Unix(), "sending", now.Add(-5*time.Minute).Unix()).
		Order("id ASC").Limit(50).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		claim := model.CoreDB().Model(&model.ProjectNotification{}).
			Where("id = ? AND ((status = ? AND available_at <= ?) OR (status = ? AND claimed_at < ?))", row.ID, "pending", now.Unix(), "sending", now.Add(-5*time.Minute).Unix()).
			Updates(map[string]interface{}{"status": "sending", "claimed_at": now.Unix(), "attempts": gorm.Expr("attempts + 1")})
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected == 0 {
			continue
		}
		previousAttempts := row.Attempts
		if err := model.CoreDB().WithContext(ctx).Where("id = ? AND status = ? AND claimed_at = ?", row.ID, "sending", now.Unix()).First(&row).Error; err != nil {
			return err
		}
		email, sendErr := row.Email()
		if sendErr == nil && email.Receiver == "" {
			sendErr = fmt.Errorf("recipient email is unavailable")
		}
		if sendErr == nil {
			sendErr = send(row, email.Subject, email.Receiver, email.HTML)
		}
		query := model.CoreDB().Model(&model.ProjectNotification{}).Where("id = ? AND status = ? AND claimed_at = ?", row.ID, "sending", now.Unix())
		if sendErr == nil {
			if err := query.Updates(map[string]interface{}{"status": "delivered", "delivered_at": time.Now().Unix(), "payload": ""}).Error; err != nil {
				return err
			}
		} else {
			// Do not log the provider error: it can echo recipient or content.
			delay := time.Minute * time.Duration(1<<min(previousAttempts, 10))
			if err := query.Updates(map[string]interface{}{"status": "pending", "available_at": now.Add(delay).Unix(), "claimed_at": nil, "first_failed_at": gorm.Expr("COALESCE(first_failed_at, ?)", now.Unix())}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
