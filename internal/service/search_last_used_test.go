package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	sqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func TestSearchLastUseOnlyTracksBusinessResponses(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:search-last-used?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	old := model.CoreDB()
	model.SetCoreDB(db)
	t.Cleanup(func() { model.SetCoreDB(old); sql, _ := db.DB(); _ = sql.Close() })
	if err = db.AutoMigrate(&model.Channel{}); err != nil {
		t.Fatal(err)
	}
	ch := model.Channel{Name: "search fixture", Type: 101}
	if err = db.Create(&ch).Error; err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"fixture"}`))
	}))
	defer server.Close()
	allowLocalFetchServer(t, server.URL)
	call := func(business bool) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		if business {
			c.Set("search_business_channel_id", ch.Id)
		}
		_, _ = doSearch(c, http.MethodGet, server.URL, 1000, nil, nil)
	}
	call(false)
	var read model.Channel
	if err = db.First(&read, ch.Id).Error; err != nil {
		t.Fatal(err)
	}
	if read.LastBusinessUsedAt != 0 {
		t.Fatal("administrative request counted as business use")
	}
	call(true)
	if err = db.First(&read, ch.Id).Error; err != nil {
		t.Fatal(err)
	}
	if read.LastBusinessUsedAt == 0 {
		t.Fatal("actual upstream response was not recorded")
	}
	future := read.LastBusinessUsedAt + 60
	db.Model(&ch).UpdateColumn("last_business_used_at", future)
	call(true)
	db.First(&read, ch.Id)
	if read.LastBusinessUsedAt != future {
		t.Fatal("out-of-order request regressed the timestamp")
	}
}
