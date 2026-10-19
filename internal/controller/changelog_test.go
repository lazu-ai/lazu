package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	gormsqlite "github.com/glebarez/sqlite"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/middleware"
	"github.com/lazu-ai/lazu/internal/model"
	"gorm.io/gorm"
)

func TestPublicChangelogOmitsProvenanceAndAllowsDocsOrigin(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open("file:changelog_public_test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Announcement{}); err != nil {
		t.Fatal(err)
	}
	previous := model.CoreDB()
	model.SetCoreDB(db)
	t.Cleanup(func() { model.SetCoreDB(previous); sqlDB, _ := db.DB(); _ = sqlDB.Close(); invalidateChangelogCache() })
	published := time.Date(2026, 9, 24, 6, 12, 0, 0, time.UTC)
	audience := []string{model.AnnouncementAudienceDocs}
	_, err = model.UpsertAnnouncement(model.AnnouncementUpsert{
		Kind: model.AnnouncementKindReleaseNote, Version: "v0.7.75", Audiences: &audience,
		TitleI18N: map[string]string{"en": "New release"}, SummaryI18N: map[string]string{"en": "Summary"},
		BodyI18N: map[string]any{"en": []string{"- First", "- Second"}}, DefaultLocale: "en",
		Source: model.AnnouncementSourceDeploy, SourceModel: "private-model", SourceCommitTo: "private-sha",
		PublishedAt: &published, PublishedAtSet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	older := published.Add(-time.Hour)
	_, err = model.UpsertAnnouncement(model.AnnouncementUpsert{
		Kind: model.AnnouncementKindOperational, Audiences: &audience,
		TitleI18N: map[string]string{"en": "Summary only"}, SummaryI18N: map[string]string{"en": "Fallback summary"},
		DefaultLocale: "en", PublishedAt: &older, PublishedAtSet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.CORS())
	router.GET("/api/public/changelog", GetPublicChangelog)
	router.GET("/api/public/changelog.md", GetPublicChangelogMarkdown)
	req := httptest.NewRequest(http.MethodGet, "/api/public/changelog?locale=en", nil)
	req.Header.Set("Origin", "http://localhost:3002")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private-model") || strings.Contains(w.Body.String(), "private-sha") || strings.Contains(w.Body.String(), "source_") {
		t.Fatalf("provenance leaked: %s", w.Body.String())
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3002" {
		t.Fatalf("CORS: %s", w.Header().Get("Access-Control-Allow-Origin"))
	}
	var page struct {
		Items []changelogItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].BodyMarkdown != "- First\n\n- Second" {
		t.Fatalf("payload: %#v", page.Items)
	}
	md := httptest.NewRecorder()
	router.ServeHTTP(md, httptest.NewRequest(http.MethodGet, "/api/public/changelog.md?locale=en", nil))
	if !strings.HasPrefix(md.Header().Get("Content-Type"), "text/markdown") || !strings.Contains(md.Body.String(), "## v0.7.75") || !strings.Contains(md.Body.String(), "Fallback summary") {
		t.Fatalf("markdown: %s %s", md.Header().Get("Content-Type"), md.Body.String())
	}
}

func TestChangelogCacheEvictsExpiredAndBoundsEntries(t *testing.T) {
	invalidateChangelogCache()
	t.Cleanup(invalidateChangelogCache)
	for i := 0; i < maxChangelogCacheEntries+25; i++ {
		writeChangelogCache(strconv.Itoa(i), changelogPage{NextBefore: uint(i)})
	}
	changelogCache.Lock()
	count := len(changelogCache.items)
	changelogCache.items["expired"] = changelogCacheEntry{expires: time.Now().Add(-time.Second)}
	changelogCache.Unlock()
	if count != maxChangelogCacheEntries {
		t.Fatalf("cache grew to %d entries", count)
	}
	if _, ok := readChangelogCache("expired"); ok {
		t.Fatal("expired entry was returned")
	}
	changelogCache.Lock()
	_, retained := changelogCache.items["expired"]
	changelogCache.Unlock()
	if retained {
		t.Fatal("expired entry was not removed")
	}
}

func TestPublishedAtBackfillRequiresRootUpdate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/announcements/:id", func(c *gin.Context) { c.Set("role", common.RoleCommonUser); AdminUpdateAnnouncement(c) })
	request := httptest.NewRequest(http.MethodPut, "/announcements/1", strings.NewReader(`{"published_at":"2026-05-28T00:00:00Z"}`))
	request.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, request)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"code":"access_denied"`) {
		t.Fatalf("non-root accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestPublishedAtBackfillRootUpdate(t *testing.T) {
	db, err := gorm.Open(gormsqlite.Open("file:changelog_root_test?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Announcement{}); err != nil {
		t.Fatal(err)
	}
	previous := model.CoreDB()
	model.SetCoreDB(db)
	t.Cleanup(func() { model.SetCoreDB(previous); sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	row, err := model.UpsertAnnouncement(model.AnnouncementUpsert{Kind: model.AnnouncementKindReleaseNote, TitleI18N: map[string]string{"en": "History"}, BodyI18N: map[string]any{"en": []string{"Old news"}}, DefaultLocale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/announcements/:id", func(c *gin.Context) { c.Set("role", common.RoleRootUser); AdminUpdateAnnouncement(c) })
	request := httptest.NewRequest(http.MethodPut, "/announcements/"+strconv.FormatUint(uint64(row.ID), 10), strings.NewReader(`{"kind":"release_note","title_i18n":{"en":"History"},"body_i18n":{"en":["Old news"]},"default_locale":"en","audiences":["docs"],"published_at":"2026-05-28T00:00:00Z"}`))
	request.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, request)
	if !strings.Contains(w.Body.String(), `"success":true`) {
		t.Fatalf("root update: %s", w.Body.String())
	}
	var saved model.Announcement
	if err := db.First(&saved, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.PublishedAt == nil || saved.PublishedAt.Format(time.RFC3339) != "2026-05-28T00:00:00Z" || string(saved.Audiences) != `["docs"]` {
		t.Fatalf("saved row: %#v", saved)
	}
}
