package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func TestProjectLogControllersPassRequestContextToReads(t *testing.T) {
	db := setupMediaJobControllerTestDB(t)
	if err := db.AutoMigrate(&model.Log{}); err != nil {
		t.Fatal(err)
	}
	old := model.ConfiguredTelemetryDB()
	model.SetTelemetryDB(db)
	t.Cleanup(func() { model.SetTelemetryDB(old) })
	user := model.User{Id: 601, Username: "log-context-owner"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	type contextKey struct{}
	for _, test := range []struct {
		name    string
		handler gin.HandlerFunc
	}{
		{"page-count", GetUserLogs}, {"search-page", SearchUserLogs},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "request-context"))
			defer cancel()
			seen := false
			callback := "test:log-context-" + test.name
			if err := db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "logs" {
					seen = tx.Statement.Context.Value(contextKey{}) == "request-context"
					cancel()
				}
			}); err != nil {
				t.Fatal(err)
			}
			defer db.Callback().Query().Remove(callback)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/log/self?page=1&page_size=5", nil).WithContext(ctx)
			c.Set("id", user.Id)
			test.handler(c)
			if !seen || recorder.Code == http.StatusOK {
				t.Fatalf("log read ignored canceled request context: seen=%t status=%d", seen, recorder.Code)
			}
		})
	}
}
