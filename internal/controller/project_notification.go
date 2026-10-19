package controller

import (
	"errors"
	"time"

	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

// Capture the wake function when scheduling, so the timer only dispatches work;
// no mail worker or database transaction is held during the grace period.
var scheduleProjectNotificationWake = func(availableAt int64) {
	time.AfterFunc(time.Until(time.Unix(availableAt, 0)), wakeProjectNotifications)
}

// Arm one wake per newly credited payment, using the persisted deadline rather
// than a fresh 60-second delay. Receipt/invoice events can still upgrade and send
// the row earlier; the claim fence makes the later wake harmless. The worker's
// periodic scan remains recovery for process restarts and delivery failures.
func scheduleProjectPaymentFallback(topupID string) error {
	var notification model.ProjectNotification
	err := model.CoreDB().Select("available_at").
		Where("event_key LIKE ? AND status = ? AND available_at > ?", "payment:"+topupID+":%", "pending", time.Now().Unix()).
		Order("available_at ASC").First(&notification).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	scheduleProjectNotificationWake(notification.AvailableAt)
	return nil
}
