package controller

import (
	"testing"
	"time"

	"github.com/lazu-ai/lazu/internal/model"
)

func TestPaymentFallbackWakeUsesNextPersistedDeadline(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	now := time.Now().Unix()
	for _, row := range []model.ProjectNotification{
		{EventKey: "payment:target:1", Status: "pending", AvailableAt: now + 7},
		{EventKey: "payment:target:2", Status: "pending", AvailableAt: now + 20},
		{EventKey: "payment:target:3", Status: "delivered", AvailableAt: now + 1},
		{EventKey: "payment:other:1", Status: "pending", AvailableAt: now + 2},
		{EventKey: "payment:ready:1", Status: "pending", AvailableAt: now},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	var scheduled []int64
	scheduleProjectNotificationWake = func(at int64) { scheduled = append(scheduled, at) }
	for _, id := range []string{"target", "ready", "missing"} {
		if err := scheduleProjectPaymentFallback(id); err != nil {
			t.Fatal(err)
		}
	}
	if len(scheduled) != 1 || scheduled[0] != now+7 {
		t.Fatalf("scheduled wrong deadline: %v", scheduled)
	}
	// Documents arrived early and the existing dispatcher already delivered it.
	if err := db.Model(&model.ProjectNotification{}).Where("event_key LIKE ?", "payment:target:%").Update("status", "delivered").Error; err != nil {
		t.Fatal(err)
	}
	if err := scheduleProjectPaymentFallback("target"); err != nil {
		t.Fatal(err)
	}
	if len(scheduled) != 1 {
		t.Fatalf("delivered payment rescheduled: %v", scheduled)
	}
}
