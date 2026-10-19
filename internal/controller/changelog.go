package controller

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
)

type changelogItem struct {
	ID             uint                  `json:"id"`
	Version        string                `json:"version,omitempty"`
	Kind           string                `json:"kind"`
	Title          string                `json:"title"`
	Summary        string                `json:"summary"`
	BodyMarkdown   string                `json:"body_markdown"`
	APIChanges     []model.APIChangeView `json:"api_changes"`
	AffectedModels []string              `json:"affected_models"`
	PublishedAt    *time.Time            `json:"published_at"`
	Locale         string                `json:"locale"`
}

type changelogPage struct {
	Items      []changelogItem `json:"items"`
	NextBefore uint            `json:"next_before"`
}

const maxChangelogCacheEntries = 256

type changelogCacheEntry struct {
	page    changelogPage
	expires time.Time
}

var changelogCache = struct {
	sync.Mutex
	items map[string]changelogCacheEntry
}{items: map[string]changelogCacheEntry{}}

func invalidateChangelogCache() {
	changelogCache.Lock()
	changelogCache.items = map[string]changelogCacheEntry{}
	changelogCache.Unlock()
	publicStatusCache.Lock()
	publicStatusCache.items = map[string]publicStatusCacheEntry{}
	publicStatusCache.Unlock()
}

func readChangelogCache(key string) (changelogPage, bool) {
	changelogCache.Lock()
	defer changelogCache.Unlock()
	entry, ok := changelogCache.items[key]
	if !ok {
		return changelogPage{}, false
	}
	if !time.Now().Before(entry.expires) {
		delete(changelogCache.items, key)
		return changelogPage{}, false
	}
	return entry.page, true
}

func writeChangelogCache(key string, page changelogPage) {
	changelogCache.Lock()
	defer changelogCache.Unlock()
	now := time.Now()
	for cachedKey, entry := range changelogCache.items {
		if !now.Before(entry.expires) {
			delete(changelogCache.items, cachedKey)
		}
	}
	if _, exists := changelogCache.items[key]; !exists && len(changelogCache.items) >= maxChangelogCacheEntries {
		oldestKey := ""
		var oldestExpiry time.Time
		for cachedKey, entry := range changelogCache.items {
			if oldestKey == "" || entry.expires.Before(oldestExpiry) {
				oldestKey, oldestExpiry = cachedKey, entry.expires
			}
		}
		delete(changelogCache.items, oldestKey)
	}
	changelogCache.items[key] = changelogCacheEntry{page: page, expires: now.Add(time.Minute)}
}

func changelogQuery(c *gin.Context, defaultLimit int) (changelogPage, error) {
	limit := defaultLimit
	if raw := c.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return changelogPage{}, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "limit", "reason": "limit must be a positive integer"})
		}
		limit = parsed
	}
	if limit > 200 {
		limit = 200
	}
	var before uint64
	if raw := c.Query("before"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return changelogPage{}, types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "before", "reason": "before must be an announcement ID"})
		}
		before = parsed
	}
	locale := requestLocale(c)
	key := fmt.Sprintf("%s:%d:%d", locale, limit, before)
	if page, ok := readChangelogCache(key); ok {
		return page, nil
	}
	views, nextBefore, err := model.ListDocsChangelog(locale, limit, uint(before))
	if err != nil {
		return changelogPage{}, err
	}
	page := changelogPage{Items: make([]changelogItem, 0, len(views)), NextBefore: nextBefore}
	for _, view := range views {
		changes := make([]model.APIChangeView, len(view.APIChanges))
		for i, change := range view.APIChanges {
			change.NoteI18N = nil
			changes[i] = change
		}
		page.Items = append(page.Items, changelogItem{
			ID: view.ID, Version: view.Version, Kind: view.Kind, Title: view.Title,
			Summary: view.Summary, BodyMarkdown: strings.Join(view.Body, "\n\n"),
			APIChanges: changes, AffectedModels: nonNilStrings(view.AffectedModels),
			PublishedAt: view.PublishedAt, Locale: resolvedChangelogLocale(view, locale),
		})
	}
	writeChangelogCache(key, page)
	return page, nil
}

func nonNilStrings(items []string) []string {
	if items == nil {
		return []string{}
	}
	return items
}

func resolvedChangelogLocale(view model.AnnouncementView, requested string) string {
	for _, candidate := range []string{requested, view.DefaultLocale, "zh", "en"} {
		if strings.TrimSpace(view.TitleI18N[candidate]) != "" {
			return candidate
		}
	}
	return requested
}

func GetPublicChangelog(c *gin.Context) {
	page, err := changelogQuery(c, 50)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

func GetPublicChangelogMarkdown(c *gin.Context) {
	page, err := changelogQuery(c, 200)
	if err != nil {
		httperr.Write(c, err)
		return
	}
	updated := time.Now().UTC().Format("2006-01-02")
	if len(page.Items) > 0 && page.Items[0].PublishedAt != nil {
		updated = page.Items[0].PublishedAt.UTC().Format("2006-01-02")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntitle: Changelog\nsource: https://lazu.ai/docs/changelog\nupdated: %s\n---\n# Changelog\n", updated)
	for _, item := range page.Items {
		heading := item.Version
		if heading == "" {
			heading = item.Title
		} else {
			heading += " — " + item.Title
		}
		fmt.Fprintf(&b, "\n## %s\n", heading)
		if item.PublishedAt != nil {
			fmt.Fprintf(&b, "%s · %s\n", item.PublishedAt.UTC().Format("2006-01-02"), item.Kind)
		}
		body := item.BodyMarkdown
		if body == "" {
			body = item.Summary
		}
		if body != "" {
			fmt.Fprintf(&b, "\n%s\n", body)
		}
		if len(item.APIChanges) > 0 {
			b.WriteString("\nAPI changes:\n")
			for _, change := range item.APIChanges {
				fmt.Fprintf(&b, "- `%s %s` — %s\n", change.Method, change.Path, change.Note)
			}
		}
	}
	c.Header("Cache-Control", "public, max-age=60")
	c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(b.String()))
}
