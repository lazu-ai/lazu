package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	neturl "net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	socialOAuthTransactionCookie = "lazu_social_oauth_tx"
	googleOAuthProvider          = "google"
	githubOAuthProvider          = "github"
)

// Browser-facing OAuth endpoints never answer with JSON: the user agent is
// mid-redirect, so failures land back on the login page as `?error=<code>`,
// an error catalog code the console renders like any other. Sentinel errors
// carry their own code; everything else is collapsed to oauth_login_failed and
// logged server-side.
var (
	errOAuthNoVerifiedEmail    = types.Err(types.ErrorCodeOAuthEmailUnverified)
	errOAuthEmailConflict      = types.Err(types.ErrorCodeOAuthEmailConflict)
	errOAuthIdentityConflict   = types.Err(types.ErrorCodeOAuthIdentityConflict)
	errOAuthRegistrationClosed = types.Err(types.ErrorCodeRegistrationClosed)
)

func oauthLoginErrorRedirect(c *gin.Context, code types.ErrorCode) {
	oauthLoginErrorRedirectNext(c, code, "")
}

// oauthLoginErrorRedirectNext sends a failed sign-in back to the door it
// started from. A sign-in headed for another surface's page returns to that
// surface's own login (keeping where it was going); everything else to the
// console's.
func oauthLoginErrorRedirectNext(c *gin.Context, code types.ErrorCode, next string) {
	target := "/login?error=" + neturl.QueryEscape(string(code))
	if next = strings.TrimSpace(next); next != "" {
		next = sanitizedOIDCNext(next)
		if surface, ok := model.UserHomeSurfaceForBrowserPath(next); ok && surface.LoginPath != "" {
			target = surface.LoginPath + "?error=" + neturl.QueryEscape(string(code)) + "&next=" + neturl.QueryEscape(next)
		}
	}
	if origin := oidcFrontendOrigin(); origin != "" {
		target = origin + target
	}
	c.Redirect(http.StatusFound, target)
}

// oauthRedirectCode keeps the codes a person can act on and hides the rest.
func oauthRedirectCode(err error) types.ErrorCode {
	switch code := types.ErrCode(err); code {
	case types.ErrorCodeOAuthEmailUnverified,
		types.ErrorCodeOAuthEmailConflict,
		types.ErrorCodeOAuthIdentityConflict,
		types.ErrorCodeRegistrationClosed:
		return code
	}
	return types.ErrorCodeOAuthLoginFailed
}

func oauthProviderErrorCode(providerError string) types.ErrorCode {
	if providerError == "access_denied" {
		return types.ErrorCodeOAuthCancelled
	}
	return types.ErrorCodeOAuthProviderError
}

var githubOAuthEndpoint = oauth2.Endpoint{
	AuthURL:  "https://github.com/login/oauth/authorize",
	TokenURL: "https://github.com/login/oauth/access_token", // secret-scan: allow -- public OAuth endpoint
}

type socialOAuthTransaction struct {
	Provider     string `json:"provider"`
	State        string `json:"state"`
	Nonce        string `json:"nonce"`
	CodeVerifier string `json:"code_verifier"`
	Next         string `json:"next"`
}

type socialIdentity struct {
	Provider    string
	Subject     string
	Username    string
	DisplayName string
	Email       string
	// MailLang is the language of the browser completing the sign-in; the
	// welcome mail for a new account is written in it.
	MailLang string
	// MailSurface is the product the browser signs up through (lz_surface);
	// the welcome mail opens it.
	MailSurface string
}

type authProviderStatus struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
}

func configuredSocialAuthProviders() []authProviderStatus {
	providers := make([]authProviderStatus, 0, 2)
	if socialOAuthConfigured(googleOAuthProvider) {
		providers = append(providers, authProviderStatus{ID: googleOAuthProvider, Provider: googleOAuthProvider})
	}
	if socialOAuthConfigured(githubOAuthProvider) {
		providers = append(providers, authProviderStatus{ID: githubOAuthProvider, Provider: githubOAuthProvider})
	}
	return providers
}

func socialOAuthConfigured(provider string) bool {
	return common.AuthProviderEnabled(provider)
}

func SocialOAuthLogin(c *gin.Context) {
	provider := strings.ToLower(strings.TrimSpace(c.Param("provider")))
	next := c.Query("next")
	oauthConfig, _, err := socialOAuthConfig(c.Request.Context(), c, provider)
	if err != nil {
		common.SysLog(fmt.Sprintf("social oauth login rejected for provider %q: %v", provider, err))
		oauthLoginErrorRedirectNext(c, types.ErrorCodeOAuthProviderNotConfigured, next)
		return
	}
	state, err := randomOIDCString()
	if err != nil {
		common.SysLog(fmt.Sprintf("social oauth state generation failed: %v", err))
		oauthLoginErrorRedirectNext(c, types.ErrorCodeOAuthLoginFailed, next)
		return
	}
	nonce, err := randomOIDCString()
	if err != nil {
		common.SysLog(fmt.Sprintf("social oauth nonce generation failed: %v", err))
		oauthLoginErrorRedirectNext(c, types.ErrorCodeOAuthLoginFailed, next)
		return
	}
	verifier := oauth2.GenerateVerifier()
	tx := socialOAuthTransaction{
		Provider:     provider,
		State:        state,
		Nonce:        nonce,
		CodeVerifier: verifier,
		Next:         sanitizedOIDCNext(next),
	}
	if err := setSocialOAuthTransaction(c, tx); err != nil {
		common.SysLog(fmt.Sprintf("social oauth transaction cookie failed: %v", err))
		oauthLoginErrorRedirectNext(c, types.ErrorCodeOAuthLoginFailed, next)
		return
	}
	options := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier)}
	if provider == googleOAuthProvider {
		options = append(options, oidc.Nonce(nonce))
	}
	c.Redirect(http.StatusFound, oauthConfig.AuthCodeURL(state, options...))
}

func SocialOAuthCallback(c *gin.Context) {
	provider := strings.ToLower(strings.TrimSpace(c.Param("provider")))
	if provider != googleOAuthProvider && provider != githubOAuthProvider {
		oauthLoginErrorRedirect(c, types.ErrorCodeOAuthProviderNotConfigured)
		return
	}
	// Read the transaction before anything can fail, so even a cancelled or
	// stale sign-in returns to the door it started from.
	tx, err := readSocialOAuthTransaction(c)
	if errMessage := strings.TrimSpace(c.Query("error")); errMessage != "" {
		clearSocialOAuthTransaction(c)
		common.SysLog(fmt.Sprintf("social oauth provider %q returned error: %s", provider, errMessage))
		oauthLoginErrorRedirectNext(c, oauthProviderErrorCode(errMessage), tx.Next)
		return
	}
	if err != nil || tx.Provider != provider || tx.State == "" || c.Query("state") != tx.State {
		clearSocialOAuthTransaction(c)
		oauthLoginErrorRedirectNext(c, types.ErrorCodeOAuthStateInvalid, tx.Next)
		return
	}
	// Consume the transaction before exchanging the one-time code. Any retry
	// starts a fresh state/nonce/PKCE flow instead of reusing callback material.
	clearSocialOAuthTransaction(c)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	oauthConfig, verifier, err := socialOAuthConfig(ctx, c, provider)
	if err != nil {
		common.SysLog(fmt.Sprintf("social oauth callback rejected for provider %q: %v", provider, err))
		oauthLoginErrorRedirectNext(c, types.ErrorCodeOAuthProviderNotConfigured, tx.Next)
		return
	}
	token, err := oauthConfig.Exchange(ctx, c.Query("code"), oauth2.VerifierOption(tx.CodeVerifier))
	if err != nil {
		common.SysLog(fmt.Sprintf("social oauth code exchange failed for provider %q: %v", provider, err))
		oauthLoginErrorRedirectNext(c, types.ErrorCodeOAuthExchangeFailed, tx.Next)
		return
	}

	var identity socialIdentity
	switch provider {
	case googleOAuthProvider:
		identity, err = googleIdentityFromToken(ctx, verifier, token, tx.Nonce)
	case githubOAuthProvider:
		identity, err = githubIdentityFromToken(ctx, oauthConfig, token)
	}
	if err != nil {
		common.SysLog(fmt.Sprintf("social oauth identity fetch failed for provider %q: %v", provider, err))
		if errors.Is(err, errOAuthNoVerifiedEmail) {
			oauthLoginErrorRedirectNext(c, types.ErrorCodeOAuthEmailUnverified, tx.Next)
		} else {
			oauthLoginErrorRedirectNext(c, types.ErrorCodeOAuthIdentityInvalid, tx.Next)
		}
		return
	}
	identity.MailLang = requestMailLang(c)
	identity.MailSurface = requestHomeSurface(c)
	user, err := resolveSocialOAuthUser(identity)
	if err != nil {
		common.SysLog(fmt.Sprintf("social oauth user resolution failed for provider %q: %v", provider, err))
		oauthLoginErrorRedirectNext(c, oauthRedirectCode(err), tx.Next)
		return
	}
	if user.Status != common.UserStatusEnabled {
		oauthLoginErrorRedirectNext(c, types.ErrorCodeUserBanned, tx.Next)
		return
	}
	if _, err := setupSession(user, c); err != nil {
		common.SysLog(fmt.Sprintf("social oauth session setup failed: %v", err))
		oauthLoginErrorRedirectNext(c, types.ErrorCodeOAuthLoginFailed, tx.Next)
		return
	}
	c.Redirect(http.StatusFound, oidcFrontendCallbackURL(tx.Next))
}

func socialOAuthConfig(ctx context.Context, c *gin.Context, provider string) (*oauth2.Config, *oidc.IDTokenVerifier, error) {
	if !socialOAuthConfigured(provider) {
		return nil, nil, errors.New("oauth provider is not configured")
	}
	redirectURL, err := socialOAuthRedirectURL(c, provider)
	if err != nil {
		return nil, nil, err
	}
	switch provider {
	case googleOAuthProvider:
		googleProvider, err := oidc.NewProvider(ctx, "https://accounts.google.com")
		if err != nil {
			return nil, nil, fmt.Errorf("load google oidc configuration: %w", err)
		}
		config := &oauth2.Config{
			ClientID:     common.GoogleOAuthClientID,
			ClientSecret: common.GoogleOAuthClientSecret,
			Endpoint:     googleProvider.Endpoint(),
			RedirectURL:  redirectURL,
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		}
		return config, googleProvider.Verifier(&oidc.Config{ClientID: common.GoogleOAuthClientID}), nil
	case githubOAuthProvider:
		return &oauth2.Config{
			ClientID:     common.GitHubOAuthClientID,
			ClientSecret: common.GitHubOAuthClientSecret,
			Endpoint:     githubOAuthEndpoint,
			RedirectURL:  redirectURL,
			Scopes:       []string{"user:email"},
		}, nil, nil
	default:
		return nil, nil, errors.New("unknown oauth provider")
	}
}

func socialOAuthRedirectURL(c *gin.Context, provider string) (string, error) {
	var configured string
	switch provider {
	case googleOAuthProvider:
		configured = common.GoogleOAuthRedirectURL
	case githubOAuthProvider:
		configured = common.GitHubOAuthRedirectURL
	default:
		return "", errors.New("unknown oauth provider")
	}
	if redirectURL := strings.TrimSpace(configured); redirectURL != "" {
		return redirectURL, nil
	}
	baseURL, err := requestBaseURL(c)
	if err != nil {
		return "", fmt.Errorf("%s oauth redirect url is required: %w", provider, err)
	}
	return baseURL + "/api/auth/oauth/" + provider + "/callback", nil
}

func googleIdentityFromToken(ctx context.Context, verifier *oidc.IDTokenVerifier, token *oauth2.Token, nonce string) (socialIdentity, error) {
	if verifier == nil {
		return socialIdentity{}, errors.New("google id token verifier is unavailable")
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return socialIdentity{}, errors.New("google id_token missing")
	}
	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return socialIdentity{}, errors.New("google id_token verification failed")
	}
	if nonce == "" || idToken.Nonce != nonce {
		return socialIdentity{}, errors.New("invalid google nonce")
	}
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return socialIdentity{}, errors.New("google claims parse failed")
	}
	email := claimString(claims, "email")
	if email == "" || !claimBool(claims, "email_verified") {
		return socialIdentity{}, errOAuthNoVerifiedEmail
	}
	return socialIdentity{
		Provider:    googleOAuthProvider,
		Subject:     idToken.Subject,
		Username:    oidcUsername(claims, email),
		DisplayName: claimString(claims, "name"),
		Email:       email,
	}, nil
}

type githubOAuthUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
}

type githubOAuthEmail struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

func githubIdentityFromToken(ctx context.Context, oauthConfig *oauth2.Config, token *oauth2.Token) (socialIdentity, error) {
	client := oauthConfig.Client(ctx, token)
	var user githubOAuthUser
	if err := getGitHubOAuthJSON(ctx, client, "https://api.github.com/user", &user); err != nil {
		return socialIdentity{}, err
	}
	if user.ID <= 0 || strings.TrimSpace(user.Login) == "" {
		return socialIdentity{}, errors.New("github returned an invalid user")
	}
	var emails []githubOAuthEmail
	if err := getGitHubOAuthJSON(ctx, client, "https://api.github.com/user/emails", &emails); err != nil {
		return socialIdentity{}, err
	}
	email := verifiedGitHubEmail(emails)
	if email == "" {
		return socialIdentity{}, errOAuthNoVerifiedEmail
	}
	displayName := strings.TrimSpace(user.Name)
	if displayName == "" {
		displayName = strings.TrimSpace(user.Login)
	}
	return socialIdentity{
		Provider:    githubOAuthProvider,
		Subject:     strconv.FormatInt(user.ID, 10),
		Username:    sanitizeOIDCUsername(user.Login),
		DisplayName: displayName,
		Email:       email,
	}, nil
}

func getGitHubOAuthJSON(ctx context.Context, client *http.Client, endpoint string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Lazu")
	res, err := client.Do(req)
	if err != nil {
		return errors.New("github api request failed")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		return fmt.Errorf("github api returned status %d", res.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(target); err != nil {
		return errors.New("github api returned an invalid response")
	}
	return nil
}

func verifiedGitHubEmail(emails []githubOAuthEmail) string {
	for _, item := range emails {
		if item.Verified && item.Primary && strings.TrimSpace(item.Email) != "" {
			return strings.TrimSpace(item.Email)
		}
	}
	for _, item := range emails {
		if item.Verified && strings.TrimSpace(item.Email) != "" {
			return strings.TrimSpace(item.Email)
		}
	}
	return ""
}

func resolveSocialOAuthUser(identity socialIdentity, _ ...string) (*model.User, error) {
	identity.Provider = strings.ToLower(strings.TrimSpace(identity.Provider))
	identity.Subject = strings.TrimSpace(identity.Subject)
	identity.Email = strings.ToLower(strings.TrimSpace(identity.Email))
	if identity.Provider == "" || identity.Subject == "" || identity.Email == "" {
		return nil, errors.New("oauth identity is incomplete")
	}
	var user *model.User
	created := false
	err := withSocialOAuthResolutionLock(model.CoreDB(), identity, func(tx *gorm.DB) error {
		var resolveErr error
		user, created, resolveErr = resolveSocialOAuthUserLocked(tx, identity)
		return resolveErr
	})
	if err != nil {
		return nil, err
	}
	if created {
		model.RecordNewUserSignupEffects(user)
		if err := initializeNewUserFeatures(user); err != nil {
			return nil, err
		}
		SendWelcomeEmail(user, socialProviderLabel(identity.Provider), common.MailLang(identity.MailLang), identity.MailSurface)
	}
	if err := model.InvalidateUserCache(user.Id); err != nil {
		common.SysLog(fmt.Sprintf("failed to invalidate oauth user cache: %v", err))
	}
	return user, nil
}

var socialOAuthResolutionStripes [128]sync.Mutex

func verifiedEmailOwnershipLockKey(email string) string {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" {
		return ""
	}
	return "oauth-email:" + normalized
}

// withSocialOAuthResolutionLock serializes both the normalized email and the
// immutable provider subject. The in-process stripes cover SQLite/self-hosted
// concurrency; PostgreSQL transaction-scoped advisory locks extend the same
// guarantee across API replicas without imposing a unique-email migration on
// legacy user rows. The callback must use the provided transaction for every
// database operation so a one-connection pool cannot deadlock and no query can
// escape the advisory-lock lifetime.
func withSocialOAuthResolutionLock(db *gorm.DB, identity socialIdentity, fn func(*gorm.DB) error) error {
	return withAccountResolutionLocks(db, []string{
		verifiedEmailOwnershipLockKey(identity.Email),
		"oauth-subject:" + identity.Provider + ":" + identity.Subject,
		externalUsernameAllocationLockKey(identity.Username),
	}, fn)
}

func externalUsernameAllocationLockKey(username string) string {
	username = sanitizeOIDCUsername(username)
	if username == "" {
		username = "oidc-user"
	}
	return "oauth-username:" + username
}

// withPasswordRegistrationLock participates in the same verified-email lock
// as every external-auth resolver. Without this shared lock, a verified
// password registration and a first OAuth callback can both observe no owner
// and commit two active users for one mailbox.
func withPasswordRegistrationLock(db *gorm.DB, email string, username string, fn func(*gorm.DB) error) error {
	return withAccountResolutionLocks(db, []string{
		verifiedEmailOwnershipLockKey(email),
		"password-username:" + strings.ToLower(strings.TrimSpace(username)),
	}, fn)
}

func withAccountResolutionLocks(db *gorm.DB, keys []string, fn func(*gorm.DB) error) error {
	if db == nil {
		return errors.New("database is not initialized")
	}
	if fn == nil {
		return errors.New("account resolution callback is required")
	}
	normalizedKeys := make([]string, 0, len(keys))
	for _, key := range keys {
		if key = strings.TrimSpace(key); key != "" {
			normalizedKeys = append(normalizedKeys, key)
		}
	}
	keys = normalizedKeys
	if len(keys) == 0 {
		return errors.New("account resolution lock key is required")
	}
	sort.Strings(keys)
	keys = compactSortedStrings(keys)
	stripeIndexes := make([]int, 0, len(keys))
	for _, key := range keys {
		hasher := fnv.New32a()
		_, _ = hasher.Write([]byte(key))
		index := int(hasher.Sum32() % uint32(len(socialOAuthResolutionStripes)))
		if len(stripeIndexes) == 0 || stripeIndexes[len(stripeIndexes)-1] != index {
			stripeIndexes = append(stripeIndexes, index)
		}
	}
	sort.Ints(stripeIndexes)
	stripeIndexes = compactSortedInts(stripeIndexes)
	for _, index := range stripeIndexes {
		socialOAuthResolutionStripes[index].Lock()
	}
	defer func() {
		for i := len(stripeIndexes) - 1; i >= 0; i-- {
			socialOAuthResolutionStripes[stripeIndexes[i]].Unlock()
		}
	}()

	return db.Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() != "postgres" {
			return fn(tx)
		}
		for _, key := range keys {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", key).Error; err != nil {
				return err
			}
		}
		return fn(tx)
	})
}

func compactSortedStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func compactSortedInts(values []int) []int {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func resolveSocialOAuthUserLocked(db *gorm.DB, identity socialIdentity) (*model.User, bool, error) {

	var storedIdentity model.UserIdentity
	err := db.Preload("User").Where("provider = ? AND subject = ?", identity.Provider, identity.Subject).First(&storedIdentity).Error
	if err == nil {
		if storedIdentity.User.Id == 0 {
			return nil, false, errors.New("oauth identity has no user")
		}
		if err := refreshSocialOAuthUser(db, &storedIdentity.User, identity); err != nil {
			return nil, false, err
		}
		if !strings.EqualFold(storedIdentity.Email, identity.Email) {
			if err := db.Model(&storedIdentity).Update("email", identity.Email).Error; err != nil {
				return nil, false, err
			}
		}
		return &storedIdentity.User, false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}

	user, err := findUserByVerifiedEmail(db, identity.Email)
	if err != nil {
		return nil, false, err
	}
	if user == nil {
		// An unrecognised provider identity signs up. Refusing here — which is
		// what an intent=login callback used to do — turned "sign in with
		// Google" into a dead end for everyone who had not already registered,
		// while the consent it was protecting is now carried by the notice
		// under the button and recorded on the row below.
		if !common.RegisterEnabled {
			return nil, false, errOAuthRegistrationClosed
		}
		user, err = createSocialOAuthUserTx(db, identity)
		if err != nil {
			return nil, false, err
		}
		return user, true, nil
	}
	newIdentity := model.UserIdentity{
		UserID:   user.Id,
		Provider: identity.Provider,
		Subject:  identity.Subject,
		Email:    identity.Email,
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&newIdentity).Error; err != nil {
		return nil, false, err
	}
	var ownerCount int64
	if err := db.Model(&model.UserIdentity{}).
		Where("provider = ? AND subject = ? AND user_id = ?", identity.Provider, identity.Subject, user.Id).
		Count(&ownerCount).Error; err != nil {
		return nil, false, err
	}
	if ownerCount != 1 {
		return nil, false, errOAuthIdentityConflict
	}
	if err := refreshSocialOAuthUser(db, user, identity); err != nil {
		return nil, false, err
	}
	return user, false, nil
}

func findUserByVerifiedEmail(db *gorm.DB, email string) (*model.User, error) {
	var users []model.User
	if err := db.Unscoped().Where("LOWER(email) = LOWER(?)", email).Limit(2).Find(&users).Error; err != nil {
		return nil, err
	}
	if len(users) > 1 {
		return nil, errOAuthEmailConflict
	}
	if len(users) == 1 {
		if users[0].DeletedAt.Valid {
			// Soft deletion intentionally reserves usernames and addresses. Do not
			// let an OAuth callback silently recreate or take over that account.
			return nil, errOAuthEmailConflict
		}
		if err := db.Model(&model.User{}).Where("id = ?", users[0].Id).Update("verified_email", strings.ToLower(strings.TrimSpace(email))).Error; err != nil {
			return nil, err
		}
		users[0].VerifiedEmail = strings.ToLower(strings.TrimSpace(email))
		return &users[0], nil
	}
	return nil, nil
}

func createSocialOAuthUser(identity socialIdentity, _ ...string) (*model.User, error) {
	identity.Provider = strings.ToLower(strings.TrimSpace(identity.Provider))
	identity.Subject = strings.TrimSpace(identity.Subject)
	identity.Email = strings.ToLower(strings.TrimSpace(identity.Email))
	if identity.Provider == "" || identity.Subject == "" || identity.Email == "" {
		return nil, errors.New("oauth identity is incomplete")
	}
	var user *model.User
	err := withSocialOAuthResolutionLock(model.CoreDB(), identity, func(tx *gorm.DB) error {
		var createErr error
		user, createErr = createSocialOAuthUserTx(tx, identity)
		return createErr
	})
	if err != nil {
		return nil, err
	}
	model.RecordNewUserSignupEffects(user)
	if err := initializeNewUserFeatures(user); err != nil {
		return nil, err
	}
	SendWelcomeEmail(user, socialProviderLabel(identity.Provider), common.MailLang(identity.MailLang), identity.MailSurface)
	return user, nil
}

func createSocialOAuthUserTx(db *gorm.DB, identity socialIdentity) (*model.User, error) {
	password, err := randomOIDCString()
	if err != nil {
		return nil, err
	}
	username, err := uniqueOIDCUsernameTx(db, identity.Username)
	if err != nil {
		return nil, err
	}
	displayName := strings.TrimSpace(identity.DisplayName)
	if displayName == "" {
		displayName = username
	}
	displayName = truncateDisplayName(displayName)
	user := &model.User{
		Username:      username,
		DisplayName:   displayName,
		Password:      password,
		Email:         identity.Email,
		VerifiedEmail: identity.Email,
		Role:          common.RoleCommonUser,
		Status:        common.UserStatusEnabled,
		TermsVersion:  common.TermsVersion,
		TermsAgreedAt: time.Now().Unix(),
	}
	// The stored password is a random placeholder, so onboarding offers setting
	// a real one as a fallback sign-in method.
	socialSetting := user.GetSetting()
	socialSetting.NeedsPassword = true
	user.SetSetting(socialSetting)
	firstIdentity := &model.UserIdentity{
		Provider: identity.Provider,
		Subject:  identity.Subject,
		Email:    identity.Email,
	}
	if err := user.InsertWithIdentityTx(db, firstIdentity); err != nil {
		return nil, err
	}
	return user, nil
}

func truncateDisplayName(displayName string) string {
	runes := []rune(displayName)
	if len(runes) <= 20 {
		return displayName
	}
	return string(runes[:20])
}

func socialProviderLabel(provider string) string {
	switch provider {
	case googleOAuthProvider:
		return "Google"
	case githubOAuthProvider:
		return "GitHub"
	default:
		return provider
	}
}

func refreshSocialOAuthUser(db *gorm.DB, user *model.User, identity socialIdentity) error {
	updates := map[string]any{}
	if strings.EqualFold(strings.TrimSpace(user.Email), strings.TrimSpace(identity.Email)) && identity.Email != "" {
		updates["verified_email"] = strings.ToLower(strings.TrimSpace(identity.Email))
		user.VerifiedEmail = strings.ToLower(strings.TrimSpace(identity.Email))
	}
	if strings.TrimSpace(user.Email) == "" {
		if err := ensureSocialOAuthEmailAvailableTx(db, identity.Email, user.Id); err != nil {
			return err
		}
		updates["email"] = identity.Email
		updates["verified_email"] = strings.ToLower(strings.TrimSpace(identity.Email))
		user.VerifiedEmail = strings.ToLower(strings.TrimSpace(identity.Email))
		user.Email = identity.Email
	}
	if strings.TrimSpace(user.DisplayName) == "" && strings.TrimSpace(identity.DisplayName) != "" {
		displayName := truncateDisplayName(strings.TrimSpace(identity.DisplayName))
		updates["display_name"] = displayName
		user.DisplayName = displayName
	}
	if len(updates) == 0 {
		return nil
	}
	return db.Model(&model.User{}).Where("id = ?", user.Id).Updates(updates).Error
}

func ensureSocialOAuthEmailAvailableTx(db *gorm.DB, email string, userID int) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil
	}
	var owners []model.User
	if err := db.Unscoped().
		Where("LOWER(email) = LOWER(?) AND id <> ?", email, userID).
		Limit(1).
		Find(&owners).Error; err != nil {
		return err
	}
	if len(owners) > 0 {
		return errOAuthEmailConflict
	}
	return nil
}

func setSocialOAuthTransaction(c *gin.Context, tx socialOAuthTransaction) error {
	encoded, err := oidcSecureCookie().Encode(socialOAuthTransactionCookie, tx)
	if err != nil {
		return err
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(socialOAuthTransactionCookie, encoded, oidcTransactionMaxAge, "/api/auth/oauth", "", requestIsSecure(c), true)
	return nil
}

func readSocialOAuthTransaction(c *gin.Context) (socialOAuthTransaction, error) {
	raw, err := c.Cookie(socialOAuthTransactionCookie)
	if err != nil {
		return socialOAuthTransaction{}, err
	}
	var tx socialOAuthTransaction
	if err := oidcSecureCookie().Decode(socialOAuthTransactionCookie, raw, &tx); err != nil {
		return socialOAuthTransaction{}, err
	}
	return tx, nil
}

func clearSocialOAuthTransaction(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(socialOAuthTransactionCookie, "", -1, "/api/auth/oauth", "", requestIsSecure(c), true)
}
