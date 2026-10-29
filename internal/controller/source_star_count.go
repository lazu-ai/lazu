package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"sync"
	"time"

	"github.com/lazu-ai/lazu/internal/common"
)

// The star count shown beside the site's star button. It is the public
// stargazers_count of the published source, read without credentials and
// kept for a while: the anonymous GitHub API allows 60 requests an hour, and
// a count a few minutes old is still the right count to show.

const (
	sourceStarCountTTL        = 10 * time.Minute
	sourceStarCountRetryAfter = time.Minute
	sourceStarCountTimeout    = 3 * time.Second
)

type sourceStarCountCache struct {
	mu         sync.Mutex
	repository string
	value      int
	known      bool
	fetchedAt  time.Time
	retryAt    time.Time
	refreshing bool
}

var sourceStarCounts = &sourceStarCountCache{}

// sourceStarCount is the cached count for the published source, and false
// when it is unknown: the source is not on GitHub, or GitHub has not
// answered yet. A stale count is served while one request refreshes it; with
// no count at all the first request waits for GitHub, briefly.
func sourceStarCount(ctx context.Context) (int, bool) {
	owner, repo, ok := sourceStarRepository()
	if !ok {
		return 0, false
	}
	repository := owner + "/" + repo
	cache := sourceStarCounts
	cache.mu.Lock()
	if cache.repository != repository {
		// A different source (a reloaded config) starts over.
		cache.repository, cache.value, cache.known = repository, 0, false
		cache.fetchedAt, cache.retryAt = time.Time{}, time.Time{}
	}
	now := time.Now()
	fresh := cache.known && now.Sub(cache.fetchedAt) < sourceStarCountTTL
	if fresh || cache.refreshing || now.Before(cache.retryAt) {
		value, known := cache.value, cache.known
		cache.mu.Unlock()
		return value, known
	}
	cache.refreshing = true
	if cache.known {
		value := cache.value
		cache.mu.Unlock()
		go cache.refresh(context.Background(), owner, repo)
		return value, true
	}
	cache.mu.Unlock()
	cache.refresh(ctx, owner, repo)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.value, cache.known
}

func (cache *sourceStarCountCache) refresh(ctx context.Context, owner string, repo string) {
	ctx, cancel := context.WithTimeout(ctx, sourceStarCountTimeout)
	defer cancel()
	count, err := fetchGitHubStarCount(ctx, owner, repo)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.refreshing = false
	if cache.repository != owner+"/"+repo {
		return
	}
	if err != nil {
		common.SysLog(fmt.Sprintf("source star count refresh failed: %v", err))
		cache.retryAt = time.Now().Add(sourceStarCountRetryAfter)
		return
	}
	cache.value, cache.known, cache.fetchedAt = count, true, time.Now()
}

func fetchGitHubStarCount(ctx context.Context, owner string, repo string) (int, error) {
	endpoint := githubAPIBaseURL + "/repos/" + neturl.PathEscape(owner) + "/" + neturl.PathEscape(repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Lazu")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, errors.New("github api request failed")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		return 0, fmt.Errorf("github repository lookup returned status %d", res.StatusCode)
	}
	var body struct {
		StargazersCount *int `json:"stargazers_count"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body); err != nil {
		return 0, fmt.Errorf("github repository lookup: %w", err)
	}
	if body.StargazersCount == nil || *body.StargazersCount < 0 {
		return 0, errors.New("github repository lookup carried no star count")
	}
	return *body.StargazersCount, nil
}
