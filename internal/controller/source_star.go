package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"golang.org/x/oauth2"
)

// Starring the published source from the site.
//
// A web page cannot star a repository for its visitor: GitHub's own star
// button is a CSRF-protected request of github.com, and embedded buttons only
// link out. What a site can do is ask once. The visitor authorizes a GitHub
// App whose only user permission is Starring, the server stars the repository
// with the token it is handed, and the token is dropped on the spot: nothing
// is stored, nothing else is read. The App is separate from GitHub sign-in so
// sign-in keeps asking for user:email and nothing more.
//
// The page opens /api/source/star/start in a popup; GitHub sends the popup
// back to /api/source/star/callback; the callback stars and redirects the
// popup to the console's /auth/source-star page, which reports to the page
// that opened it and closes. Without a popup (blocked, or a plain navigation)
// the same page returns to where the visitor came from with ?star=<result>.

const (
	sourceStarTransactionCookie = "lazu_source_star_tx"
	sourceStarCallbackPath      = "/auth/source-star"

	sourceStarResultStarred   = "starred"
	sourceStarResultAlready   = "already"
	sourceStarResultCancelled = "cancelled"
	sourceStarResultFailed    = "failed"
)

// Overridden by tests, which stand in for GitHub.
var (
	githubStarEndpoint = oauth2.Endpoint{
		AuthURL:  "https://github.com/login/oauth/authorize",
		TokenURL: "https://github.com/login/oauth/access_token", // secret-scan: allow -- public OAuth endpoint
	}
	githubAPIBaseURL = "https://api.github.com"
)

var githubRepositoryURL = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9-]+)/([A-Za-z0-9._-]+?)(?:\.git)?/?$`)

type sourceStarTransaction struct {
	State  string `json:"state"`
	Return string `json:"return"`
	// Popup is set when the page opened the flow in a popup, which the
	// console's star page then closes instead of navigating.
	Popup bool `json:"popup"`
}

// sourceStarRepository is the owner and name of the published source when it
// is a GitHub repository; ok is false for any other source link.
func sourceStarRepository() (owner string, repo string, ok bool) {
	match := githubRepositoryURL.FindStringSubmatch(common.SiteSourceURL())
	if match == nil {
		return "", "", false
	}
	return match[1], match[2], true
}

// sourceStarEnabled is true when a deployment has both a GitHub repository to
// star and a GitHub App to star it with.
func sourceStarEnabled() bool {
	if strings.TrimSpace(common.GitHubStarClientID) == "" || strings.TrimSpace(common.GitHubStarClientSecret) == "" {
		return false
	}
	_, _, ok := sourceStarRepository()
	return ok
}

func sourceStarConfig(c *gin.Context) (*oauth2.Config, error) {
	if !sourceStarEnabled() {
		return nil, errors.New("source star is not configured")
	}
	redirectURL := strings.TrimSpace(common.GitHubStarRedirectURL)
	if redirectURL == "" {
		baseURL, err := requestBaseURL(c)
		if err != nil {
			return nil, fmt.Errorf("source star redirect url is required: %w", err)
		}
		redirectURL = baseURL + "/api/source/star/callback"
	}
	// A GitHub App's permissions are fixed on the App; the authorization
	// carries no scope.
	return &oauth2.Config{
		ClientID:     common.GitHubStarClientID,
		ClientSecret: common.GitHubStarClientSecret,
		Endpoint:     githubStarEndpoint,
		RedirectURL:  redirectURL,
	}, nil
}

// sanitizedSourceStarReturn keeps the page the visitor came from, as a path
// on this site; anything else returns to the home page.
func sanitizedSourceStarReturn(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "/"
	}
	if clean := sanitizedOIDCNext(raw); clean == raw {
		return clean
	}
	return "/"
}

func sourceStarFinish(c *gin.Context, result string, returnPath string, popup bool) {
	target := sourceStarCallbackPath + "?result=" + neturl.QueryEscape(result) + "&return=" + neturl.QueryEscape(sanitizedSourceStarReturn(returnPath))
	if popup {
		target += "&popup=1"
	}
	if origin := oidcFrontendOrigin(); origin != "" {
		target = origin + target
	}
	c.Redirect(http.StatusFound, target)
}

// SourceStarStatus tells a public page where the deployment's source is
// published and whether the site can star it for the visitor; the public
// pages are prerendered and ask this rather than the console's /api/status,
// so a fork's GitHub entry points at the fork. The answer changes with a
// restart, so it is cacheable for a while.
func SourceStarStatus(c *gin.Context) {
	c.Header("Cache-Control", "public, max-age=300")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"enabled":    sourceStarEnabled(),
			"source_url": common.SiteSourceURL(),
		},
	})
}

// SourceStarStart sends the browser to GitHub to authorize the star. The
// state lives in a signed cookie scoped to the callback, as sign-in does.
func SourceStarStart(c *gin.Context) {
	returnPath := c.Query("return")
	popup := c.Query("popup") == "1"
	config, err := sourceStarConfig(c)
	if err != nil {
		common.SysLog(fmt.Sprintf("source star start rejected: %v", err))
		sourceStarFinish(c, sourceStarResultFailed, returnPath, popup)
		return
	}
	state, err := randomOIDCString()
	if err != nil {
		common.SysLog(fmt.Sprintf("source star state generation failed: %v", err))
		sourceStarFinish(c, sourceStarResultFailed, returnPath, popup)
		return
	}
	tx := sourceStarTransaction{State: state, Return: sanitizedSourceStarReturn(returnPath), Popup: popup}
	encoded, err := oidcSecureCookie().Encode(sourceStarTransactionCookie, tx)
	if err != nil {
		common.SysLog(fmt.Sprintf("source star transaction cookie failed: %v", err))
		sourceStarFinish(c, sourceStarResultFailed, returnPath, popup)
		return
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(sourceStarTransactionCookie, encoded, oidcTransactionMaxAge, "/api/source/star", "", requestIsSecure(c), true)
	c.Redirect(http.StatusFound, config.AuthCodeURL(state))
}

// SourceStarCallback is where GitHub returns the browser. It stars the
// repository with the one-time token and reports the outcome to the page.
func SourceStarCallback(c *gin.Context) {
	tx, txErr := readSourceStarTransaction(c)
	clearSourceStarTransaction(c)
	if providerError := strings.TrimSpace(c.Query("error")); providerError != "" {
		common.SysLog(fmt.Sprintf("source star authorization returned error: %s", providerError))
		result := sourceStarResultFailed
		if providerError == "access_denied" {
			result = sourceStarResultCancelled
		}
		sourceStarFinish(c, result, tx.Return, tx.Popup)
		return
	}
	if txErr != nil || tx.State == "" || c.Query("state") != tx.State {
		sourceStarFinish(c, sourceStarResultFailed, tx.Return, tx.Popup)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	config, err := sourceStarConfig(c)
	if err != nil {
		common.SysLog(fmt.Sprintf("source star callback rejected: %v", err))
		sourceStarFinish(c, sourceStarResultFailed, tx.Return, tx.Popup)
		return
	}
	token, err := config.Exchange(ctx, c.Query("code"))
	if err != nil {
		common.SysLog(fmt.Sprintf("source star code exchange failed: %v", err))
		sourceStarFinish(c, sourceStarResultFailed, tx.Return, tx.Popup)
		return
	}
	result, err := starSourceRepository(ctx, config.Client(ctx, token))
	if err != nil {
		common.SysLog(fmt.Sprintf("source star failed: %v", err))
		sourceStarFinish(c, sourceStarResultFailed, tx.Return, tx.Popup)
		return
	}
	sourceStarFinish(c, result, tx.Return, tx.Popup)
}

// starSourceRepository stars the published source for the token's user. A
// star already in place is reported as such rather than set again: the PUT
// answers 204 either way, so only the prior check can tell.
func starSourceRepository(ctx context.Context, client *http.Client) (string, error) {
	owner, repo, ok := sourceStarRepository()
	if !ok {
		return "", errors.New("published source is not a github repository")
	}
	endpoint := githubAPIBaseURL + "/user/starred/" + neturl.PathEscape(owner) + "/" + neturl.PathEscape(repo)
	status, err := githubStarRequest(ctx, client, http.MethodGet, endpoint)
	if err != nil {
		return "", err
	}
	if status == http.StatusNoContent {
		return sourceStarResultAlready, nil
	}
	if status != http.StatusNotFound {
		return "", fmt.Errorf("github star check returned status %d", status)
	}
	status, err = githubStarRequest(ctx, client, http.MethodPut, endpoint)
	if err != nil {
		return "", err
	}
	if status != http.StatusNoContent {
		return "", fmt.Errorf("github star returned status %d", status)
	}
	return sourceStarResultStarred, nil
}

func githubStarRequest(ctx context.Context, client *http.Client, method string, endpoint string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Lazu")
	if method == http.MethodPut {
		// GitHub requires a zero Content-Length on this bodiless PUT.
		req.ContentLength = 0
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, errors.New("github api request failed")
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	return res.StatusCode, nil
}

func readSourceStarTransaction(c *gin.Context) (sourceStarTransaction, error) {
	raw, err := c.Cookie(sourceStarTransactionCookie)
	if err != nil {
		return sourceStarTransaction{}, err
	}
	var tx sourceStarTransaction
	if err := oidcSecureCookie().Decode(sourceStarTransactionCookie, raw, &tx); err != nil {
		return sourceStarTransaction{}, err
	}
	return tx, nil
}

func clearSourceStarTransaction(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(sourceStarTransactionCookie, "", -1, "/api/source/star", "", requestIsSecure(c), true)
}
